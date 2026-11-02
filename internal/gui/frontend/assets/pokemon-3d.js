import * as THREE from "./vendor/three.module.min.js";
import { GLTFLoader } from "./vendor/GLTFLoader.js";
import { DRACOLoader } from "./vendor/DRACOLoader.js";

const TARGET_HEIGHT = 2.35;
const TARGET_WIDTH = 2.9;
const TARGET_DEPTH = 3.8;
const EVOLUTION_FADE_MS = 1500;
// 头部跟随鼠标的响应曲线。取值偏小/偏大会分别表现为「慢半拍」和「抽搐」，
// 这里按 60fps 下约 90-130ms 收到位来定。
const LOOK_SMOOTH_TIME_X = 0.1;
const LOOK_SMOOTH_TIME_Y = 0.12;
// 按住满 CHARGE_FULL_SECONDS 秒即视为「蓄满」，继续按住只维持满蓄力状态。
const CHARGE_FULL_SECONDS = 0.9;
const BODY_FOLLOW_LAMBDA = 4.8;
const HEAD_FOLLOW_LAMBDA = 16;
const NECK_FOLLOW_LAMBDA = 11;

function damp(current, target, lambda, delta) {
  return THREE.MathUtils.lerp(current, target, 1 - Math.exp(-lambda * delta));
}

function smoothDamp(current, velocity, target, smoothTime, delta) {
  const omega = 2 / Math.max(0.001, smoothTime);
  const x = omega * delta;
  const decay = 1 / (1 + x + 0.48 * x * x + 0.235 * x * x * x);
  const change = current - target;
  const temp = (velocity + omega * change) * delta;
  return {
    value: target + (change + temp) * decay,
    velocity: (velocity - omega * temp) * decay,
  };
}

function createContactShadow() {
  const canvas = document.createElement("canvas");
  canvas.width = 256;
  canvas.height = 128;
  const context = canvas.getContext("2d");
  const gradient = context.createRadialGradient(128, 64, 8, 128, 64, 118);
  gradient.addColorStop(0, "rgba(20, 18, 24, .32)");
  gradient.addColorStop(0.5, "rgba(20, 18, 24, .14)");
  gradient.addColorStop(1, "rgba(20, 18, 24, 0)");
  context.fillStyle = gradient;
  context.fillRect(0, 0, 256, 128);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

function materialList(root) {
  const list = [];
  root.traverse(function (object) {
    if (!object.material) return;
    const materials = Array.isArray(object.material) ? object.material : [object.material];
    materials.forEach(function (material) {
      if (!list.includes(material)) list.push(material);
    });
  });
  return list;
}

function disposeObject(root) {
  if (!root) return;
  root.traverse(function (object) {
    if (object.geometry) object.geometry.dispose();
    if (!object.material) return;
    const materials = Array.isArray(object.material) ? object.material : [object.material];
    materials.forEach(function (material) {
      if (material.map) material.map.dispose();
      if (material.normalMap) material.normalMap.dispose();
      if (material.roughnessMap) material.roughnessMap.dispose();
      if (material.metalnessMap) material.metalnessMap.dispose();
      material.dispose();
    });
  });
}

function findRigBones(root) {
  const all = [];
  root.traverse(function (object) {
    if (object.isBone || /head|neck/i.test(object.name || "")) all.push(object);
  });
  const heads = all.filter(function (object) {
    return /head/i.test(object.name || "") && !/endhead/i.test(object.name || "");
  });
  const necks = all.filter(function (object) {
    return /neck/i.test(object.name || "");
  });
  const head = heads.find(function (object) {
    return /^head(?:_\d+)?$/i.test(object.name || "");
  }) || heads[0] || null;
  let neck = necks.find(function (object) {
    return /neck3|neck_?0?3/i.test(object.name || "");
  });
  if (!neck) neck = necks[necks.length - 1] || null;
  return { head, neck };
}

function captureBone(bone) {
  if (!bone) return null;
  return {
    bone,
    base: bone.quaternion.clone(),
  };
}

function applyBoneAim(rig, aimX, aimY, delta) {
  const targetYaw = aimX * 0.62;
  const targetPitch = aimY * 0.3;
  const targetRoll = -aimX * 0.06;
  const local = new THREE.Quaternion().setFromEuler(
    new THREE.Euler(targetPitch, targetYaw, targetRoll, "YXZ")
  );
  // 头承担大部分转向、脖子分担一部分，整体才不会像一块木板在转。
  const neckPart = new THREE.Quaternion().copy(local).slerp(
    new THREE.Quaternion(),
    0.52
  );
  const headPart = new THREE.Quaternion().copy(local).slerp(
    new THREE.Quaternion(),
    0.14
  );
  const headAlpha = 1 - Math.exp(-HEAD_FOLLOW_LAMBDA * delta);
  const neckAlpha = 1 - Math.exp(-NECK_FOLLOW_LAMBDA * delta);
  [rig.neck && { entry: rig.neck, part: neckPart, alpha: neckAlpha },
    rig.head && { entry: rig.head, part: headPart, alpha: headAlpha }].forEach(function (item) {
    if (!item || !item.entry) return;
    const entry = item.entry;
    const desired = entry.base.clone().multiply(item.part);
    entry.bone.quaternion.slerp(desired, item.alpha);
  });
}

// 少量模型的绑定姿势是展开的 T-pose，直接摆在桌面上过于僵硬。
// 这里按传入的 { 骨骼名: [x, y, z] } 追加一次局部旋转，把姿势调自然。
function applyBoneAdjust(source, adjust) {
  if (!adjust) return;
  const keys = Object.keys(adjust);
  const euler = new THREE.Euler();
  source.traverse(function (object) {
    if (!object.isBone) return;
    const name = object.name || "";
    // 不同来源的模型骨骼命名不一致（LShoulder / LShoulder_59），
    // 因此按「完全相等或名称为前缀」匹配，同一套配置可以复用到多个模型。
    const key = keys.find(function (candidate) {
      return name === candidate || name.indexOf(candidate) === 0;
    });
    if (!key) return;
    const spec = adjust[key];
    if (!Array.isArray(spec)) return;
    euler.set(
      Number(spec[0]) || 0,
      Number(spec[1]) || 0,
      Number(spec[2]) || 0,
      "XYZ"
    );
    object.quaternion.multiply(
      new THREE.Quaternion().setFromEuler(euler)
    );
  });
  source.updateMatrixWorld(true);
}

function prepareModel(source) {
  source.updateMatrixWorld(true);
  const initial = new THREE.Box3().setFromObject(source, true);
  const initialSize = initial.getSize(new THREE.Vector3());
  const scale = Math.min(
    TARGET_HEIGHT / Math.max(0.001, initialSize.y),
    TARGET_WIDTH / Math.max(0.001, initialSize.x),
    TARGET_DEPTH / Math.max(0.001, initialSize.z)
  );
  source.scale.setScalar(scale);
  source.updateMatrixWorld(true);

  const scaled = new THREE.Box3().setFromObject(source, true);
  const center = scaled.getCenter(new THREE.Vector3());
  source.position.x -= center.x;
  source.position.y -= scaled.min.y;
  source.updateMatrixWorld(true);

  source.traverse(function (object) {
    if (!object.isMesh) return;
    object.frustumCulled = false;
    object.castShadow = false;
    object.receiveShadow = false;
  });
  return source;
}

function createStageViewer(canvas, options) {
  const settings = options || {};
  const models = Array.isArray(settings.models) ? settings.models : [];
  const reduceMotion = !!(window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  let stage = Math.max(1, Math.min(models.length, Number(settings.stage) || 1));
  let enabled = settings.enabled !== false;
  let closed = false;
  let loadingToken = 0;
  let animationFrame = 0;
  let lastTime = performance.now();
  let elapsed = 0;
  let aimX = 0;
  let aimY = 0;
  let currentAimX = 0;
  let currentAimY = 0;
  let aimVelocityX = 0;
  let aimVelocityY = 0;
  let bodyAimX = 0;
  let bodyAimY = 0;
  let current = null;
  let currentRig = null;
  let transition = null;
  let effectIntensity = Math.max(1, Math.min(5, Number(settings.effectIntensity) || 1));
  let skillStart = -1;
  let skillType = "";
  let skillPower = 1;
  let skillUltimate = false;
  // 长按蓄力：chargeStart>=0 表示正在蓄力，0..1 的进度驱动下蹲、抖动与自发光。
  let chargeStart = -1;
  let chargeGlow = 0;
  let resizeObserver = null;

  const renderer = new THREE.WebGLRenderer({
    canvas,
    alpha: true,
    antialias: true,
    powerPreference: "low-power",
  });
  renderer.setClearColor(0x000000, 0);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.12;
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.6));

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(30, 1, 0.1, 40);
  camera.position.set(0, 0.2, 5.35);
  camera.lookAt(0, 0.05, 0);

  scene.add(new THREE.HemisphereLight(0xfffaf0, 0x263140, 2.7));
  const key = new THREE.DirectionalLight(0xffe4bb, 3.8);
  key.position.set(-3.5, 5.2, 4.6);
  scene.add(key);
  const fill = new THREE.DirectionalLight(0xbed8ff, 2.4);
  fill.position.set(4, 2.2, 3.4);
  scene.add(fill);
  const rim = new THREE.DirectionalLight(0xffc06a, 2.25);
  rim.position.set(-2.6, 2.1, -3.5);
  scene.add(rim);

  const shadow = new THREE.Mesh(
    new THREE.PlaneGeometry(3.1, 1.15),
    new THREE.MeshBasicMaterial({
      map: createContactShadow(),
      transparent: true,
      depthWrite: false,
      opacity: 0.72,
    })
  );
  shadow.rotation.x = -Math.PI / 2;
  shadow.position.set(0, -1.16, 0.08);
  scene.add(shadow);

  const draco = new DRACOLoader();
  draco.setDecoderPath(new URL("./assets/vendor/draco/", document.baseURI).href);
  const loader = new GLTFLoader();
  loader.setDRACOLoader(draco);

  function notifyFallback() {
    if (settings.onUnavailable) settings.onUnavailable();
  }

  function renderOnce() {
    if (!closed) renderer.render(scene, camera);
  }

  function resize() {
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, Math.round(rect.width));
    const height = Math.max(1, Math.round(rect.height));
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.6));
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    renderOnce();
  }

  function addLoadedModel(root, sceneStage) {
    const rotation = settings.modelRotations && settings.modelRotations[sceneStage - 1];
    if (Array.isArray(rotation)) {
      root.rotation.set(
        Number(rotation[0]) || 0,
        Number(rotation[1]) || 0,
        Number(rotation[2]) || 0
      );
    }
    applyBoneAdjust(root, settings.boneAdjust);
    prepareModel(root);
    const rig = findRigBones(root);
    const holder = new THREE.Group();
    holder.add(root);
    holder.position.y = -1.1;
    holder.userData.stage = sceneStage;
    root.rotation.y = settings.modelYaw || 0;
    scene.add(holder);
    const materials = materialList(root);
    materials.forEach(function (material) {
      // 低模导出常带着 material metallic=0.4 的默认值，又没有任何金属度贴图。
      // 场景里没有环境贴图时，这部分金属度只会把固有色压暗发灰，所以统一
      // 压成哑光、抬高粗糙度，让宝可梦回到平涂卡通的观感。
      if (material.metalness !== undefined && !material.metalnessMap) {
        material.metalness = 0;
      }
      if (material.roughness !== undefined &&
          !material.roughnessMap && material.roughness < 0.78) {
        material.roughness = 0.78;
      }
      if (material.envMapIntensity !== undefined) {
        material.envMapIntensity = 0.65;
      }
      material.userData.qkaOpacity = Number.isFinite(material.opacity) ? material.opacity : 1;
      material.userData.qkaTransparent = !!material.transparent;
      material.userData.qkaDepthWrite = material.depthWrite !== false;
      material.transparent = material.userData.qkaTransparent;
      material.needsUpdate = true;
    });
    return {
      root,
      holder,
      rig: {
        head: captureBone(rig.head),
        neck: captureBone(rig.neck),
      },
      materials,
      baseY: holder.position.y,
    };
  }

  function setOpacity(entry, opacity) {
    entry.materials.forEach(function (material) {
      const value = Math.max(0, Math.min(1, Number(opacity) || 0));
      material.opacity = material.userData.qkaOpacity * value;
      const transparent = material.userData.qkaTransparent || value < 0.999;
      const depthWrite = value >= 0.999 ? material.userData.qkaDepthWrite : false;
      if (material.transparent !== transparent || material.depthWrite !== depthWrite) {
        material.transparent = transparent;
        material.depthWrite = depthWrite;
        material.needsUpdate = true;
      }
    });
  }

  async function loadStage(nextStage) {
    const url = models[nextStage - 1];
    if (!url) throw new Error("missing pokemon model");
    const gltf = await loader.loadAsync(url);
    return addLoadedModel(gltf.scene, nextStage);
  }

  async function setStage(nextStage, animate) {
    const targetStage = Math.max(1, Math.min(models.length, Number(nextStage) || 1));
    if (targetStage === stage && current) return;
    stage = targetStage;
    const token = ++loadingToken;
    if (settings.onLoading) settings.onLoading(true);
    try {
      const next = await loadStage(targetStage);
      if (closed || token !== loadingToken) {
        disposeObject(next.root);
        return;
      }
      setOpacity(next, 0);
      next.holder.scale.setScalar(0.82);
      if (!current) {
        current = next;
        currentRig = next.rig;
        setOpacity(next, 1);
        next.holder.scale.setScalar(1);
        if (settings.onReady) settings.onReady();
      } else {
        transition = {
          from: current,
          to: next,
          start: performance.now(),
          duration: animate === false || reduceMotion ? 0 : EVOLUTION_FADE_MS,
        };
        current = next;
        currentRig = next.rig;
      }
      renderOnce();
    } catch (error) {
      if (token === loadingToken && settings.onError) settings.onError(error);
      notifyFallback();
    } finally {
      if (token === loadingToken && settings.onLoading) settings.onLoading(false);
    }
  }

  function applySkill(time) {
    if (!current || skillStart < 0) return;
    // 大招比普通技能长 1.7 倍，落地后还有一次二次冲击，读起来更像「放了个大的」。
    const base = 520 + skillPower * 56;
    const duration = base * (skillUltimate ? 1.7 : 1);
    const progress = (time - skillStart) / duration;
    if (progress >= 1) {
      skillStart = -1;
      skillUltimate = false;
      return;
    }
    const envelope = Math.sin(progress * Math.PI);
    const amplify = skillUltimate ? 1.45 : 1;
    const wave = Math.sin(progress * Math.PI * (skillType === "electric" ? 13 : 5));
    const holder = current.holder;
    holder.position.y = current.baseY +
      Math.abs(wave) * 0.055 * skillPower * envelope * amplify;
    holder.rotation.z = wave * 0.035 * skillPower * envelope * amplify;
    holder.rotation.y = -wave * 0.025 * skillPower * envelope * amplify;
    const pulse = 1 + envelope * (0.025 + skillPower * 0.012) * amplify;
    holder.scale.setScalar(pulse);
    if (skillUltimate) {
      // 二次冲击：后半段再叠一个快速衰减的纵向弹跳。
      const settle = Math.max(0, (progress - 0.55) / 0.45);
      const recoil = Math.sin(settle * Math.PI) * (1 - settle);
      holder.position.y += recoil * 0.09 * skillPower;
      holder.scale.setScalar(pulse - recoil * 0.05);
    }
    if (skillType === "electric") {
      holder.position.x = wave * 0.038 * skillPower * envelope * amplify;
    } else if (skillType === "water") {
      holder.scale.set(
        1 + envelope * 0.04 * skillPower * amplify,
        1 - envelope * 0.025 * amplify,
        1
      );
    } else if (skillType === "leaf") {
      holder.rotation.z += Math.sin(progress * Math.PI * 8) * 0.035 * envelope * amplify;
    }
  }

  // applyCharge 在蓄力期间接管姿态：下蹲、轻微抖动、自发光渐强。
  // 长按越久 glow 越足，玩家能看出「再按住就要放大招了」。
  function applyCharge(time) {
    if (!current || chargeStart < 0) return;
    const held = Math.max(0, (time - chargeStart) / 1000);
    chargeGlow = Math.min(1, held / CHARGE_FULL_SECONDS);
    const holder = current.holder;
    const tremble = Math.sin(held * 26) * 0.012 * chargeGlow;
    const crouch = 0.075 * chargeGlow;
    holder.position.y = current.baseY - crouch;
    holder.rotation.z = tremble;
    holder.rotation.x = 0;
    const squash = 1 + chargeGlow * 0.04;
    holder.scale.set(squash, 1 - chargeGlow * 0.06, squash);
    // 自发光只做「边缘发亮」的提示，不能把固有色冲掉：颜色降低饱和度、
    // 强度控制在基础值的 +0.65 以内，蓄满时仍看得清五官和花纹。
    const glow = chargeGlow * 0.22;
    current.materials.forEach(function (material) {
      if (!material.emissive) return;
      if (material.userData.qkaBaseEmissive === undefined) {
        material.userData.qkaBaseEmissive = material.emissive.getHex();
        material.userData.qkaBaseEmissiveIntensity =
          Number.isFinite(material.emissiveIntensity) ? material.emissiveIntensity : 1;
      }
      const accent = new THREE.Color(settings.chargeColor || 0xffc46b);
      accent.lerp(new THREE.Color(0x2a1a08), 0.55);
      material.emissive.copy(accent);
      material.emissiveIntensity =
        material.userData.qkaBaseEmissiveIntensity + glow * 0.45;
      material.needsUpdate = true;
    });
  }

  function clearChargeGlow() {
    if (!current) return;
    current.materials.forEach(function (material) {
      if (!material.emissive || material.userData.qkaBaseEmissive === undefined) return;
      material.emissive.setHex(material.userData.qkaBaseEmissive);
      material.emissiveIntensity = material.userData.qkaBaseEmissiveIntensity;
      material.needsUpdate = true;
    });
    chargeGlow = 0;
  }

  function applyFrame(time, delta) {
    if (!current) return;
    const nextX = smoothDamp(
      currentAimX,
      aimVelocityX,
      aimX,
      LOOK_SMOOTH_TIME_X,
      delta
    );
    const nextY = smoothDamp(
      currentAimY,
      aimVelocityY,
      aimY,
      LOOK_SMOOTH_TIME_Y,
      delta
    );
    currentAimX = nextX.value;
    aimVelocityX = nextX.velocity;
    currentAimY = nextY.value;
    aimVelocityY = nextY.velocity;
    bodyAimX = damp(bodyAimX, currentAimX, BODY_FOLLOW_LAMBDA, delta);
    bodyAimY = damp(bodyAimY, currentAimY, BODY_FOLLOW_LAMBDA * 0.82, delta);
    const holder = current.holder;
    holder.position.y = current.baseY;
    holder.position.x = 0;
    holder.rotation.set(0, 0, 0);
    holder.scale.setScalar(1);
    if (chargeStart >= 0) {
      applyCharge(time);
      if (currentRig) applyBoneAim(currentRig, currentAimX * 0.35, currentAimY * 0.35, delta);
      if (skillStart >= 0) applySkill(time);
      return;
    }
    const breathing = reduceMotion ? 0 : Math.sin(elapsed * 1.55) * 0.008;
    const idleNod = reduceMotion ? 0 : Math.sin(elapsed * 1.9 + 0.6) * 0.004;
    holder.position.y += breathing;
    holder.rotation.y += bodyAimX * 0.095;
    holder.rotation.x -= bodyAimY * 0.025 + idleNod;
    holder.rotation.z -= currentAimX * 0.008;
    if (currentRig) applyBoneAim(currentRig, currentAimX, currentAimY, delta);
    applySkill(time);

    if (transition) {
      const p = transition.duration <= 0
        ? 1
        : Math.min(1, (time - transition.start) / transition.duration);
      const eased = 1 - Math.pow(1 - p, 3);
      setOpacity(transition.from, 1 - eased);
      setOpacity(transition.to, eased);
      transition.from.holder.scale.setScalar(1 - eased * 0.16);
      transition.to.holder.scale.multiplyScalar(0.82 + eased * 0.18);
      if (p >= 1) {
        disposeObject(transition.from.root);
        setOpacity(transition.to, 1);
        transition.to.holder.scale.setScalar(1);
        transition = null;
      }
    }
  }

  function frame(time) {
    if (!enabled || closed) {
      animationFrame = 0;
      return;
    }
    const delta = Math.min(0.05, Math.max(0.001, (time - lastTime) / 1000));
    lastTime = time;
    elapsed += delta;
    applyFrame(time, delta);
    renderOnce();
    animationFrame = requestAnimationFrame(frame);
  }

  function startLoop() {
    if (!enabled || closed || animationFrame) return;
    lastTime = performance.now();
    animationFrame = requestAnimationFrame(frame);
  }

  function stopLoop() {
    if (!animationFrame) return;
    cancelAnimationFrame(animationFrame);
    animationFrame = 0;
  }

  function setEnabled(nextEnabled) {
    enabled = !!nextEnabled;
    if (enabled) startLoop();
    else stopLoop();
  }

  function setPointer(x, y) {
    aimX = THREE.MathUtils.clamp(Number(x) || 0, -1, 1);
    aimY = THREE.MathUtils.clamp(Number(y) || 0, -1, 1);
    if (reduceMotion) {
      currentAimX = aimX;
      currentAimY = aimY;
      aimVelocityX = 0;
      aimVelocityY = 0;
      bodyAimX = aimX;
      bodyAimY = aimY;
      applyFrame(performance.now(), 1);
      renderOnce();
    }
  }

  function resetPointer() {
    aimX = 0;
    aimY = 0;
  }

  function playSkill(type, intensity, options) {
    const ultimate = !!(options && options.ultimate);
    if (chargeStart >= 0) {
      chargeStart = -1;
      clearChargeGlow();
    }
    skillType = type || "electric";
    skillPower = ultimate
      ? 5
      : Math.max(1, Math.min(5, Number(intensity) || effectIntensity));
    effectIntensity = Math.max(effectIntensity, skillPower);
    skillUltimate = ultimate;
    skillStart = performance.now();
  }

  // setCharging 由长短按逻辑驱动：true 进入蓄力姿态，false 取消并复位。
  function setCharging(on) {
    const next = !!on;
    if (next === (chargeStart >= 0)) return;
    chargeStart = next ? performance.now() : -1;
    if (!next) clearChargeGlow();
  }

  function isCharging() {
    return chargeStart >= 0;
  }

  function chargeProgress() {
    if (chargeStart < 0) return 0;
    return Math.min(1, (performance.now() - chargeStart) / (CHARGE_FULL_SECONDS * 1000));
  }

  function setEffectIntensity(value) {
    effectIntensity = Math.max(1, Math.min(5, Number(value) || 1));
  }

  function dispose() {
    if (closed) return;
    closed = true;
    stopLoop();
    if (resizeObserver) resizeObserver.disconnect();
    if (current) disposeObject(current.root);
    if (transition) disposeObject(transition.from.root);
    scene.traverse(function (object) {
      if (object.geometry) object.geometry.dispose();
      if (object.material) {
        if (object.material.map) object.material.map.dispose();
        object.material.dispose();
      }
    });
    draco.dispose();
    renderer.dispose();
  }

  resize();
  if (window.ResizeObserver) {
    resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(canvas);
  } else {
    window.addEventListener("resize", resize);
  }
  canvas.addEventListener("webglcontextlost", function (event) {
    event.preventDefault();
    stopLoop();
    notifyFallback();
  }, { once: true });
  setStage(stage, false);
  startLoop();

  return {
    setStage,
    setEnabled,
    setPointer,
    resetPointer,
    playSkill,
    setCharging,
    isCharging,
    chargeProgress,
    setEffectIntensity,
    resize,
    dispose,
    debugState: function () {
      return {
        aimX,
        aimY,
        headBone: currentRig && currentRig.head ? currentRig.head.bone.name : "",
        neckBone: currentRig && currentRig.neck ? currentRig.neck.bone.name : "",
        headQuaternion: currentRig && currentRig.head
          ? currentRig.head.bone.quaternion.toArray()
          : [],
        currentAimX,
        currentAimY,
        aimVelocityX,
        aimVelocityY,
      };
    },
    get stage() {
      return stage;
    },
  };
}

export function createPokemon3D(canvas, options) {
  if (!canvas) throw new Error("missing pokemon canvas");
  try {
    return createStageViewer(canvas, options);
  } catch (error) {
    if (options && options.onUnavailable) options.onUnavailable(error);
    return {
      setStage: function () {},
      setEnabled: function () {},
      setPointer: function () {},
      resetPointer: function () {},
      playSkill: function () {},
      setCharging: function () {},
      isCharging: function () { return false; },
      chargeProgress: function () { return 0; },
      setEffectIntensity: function () {},
      resize: function () {},
      dispose: function () {},
      stage: 1,
    };
  }
}
