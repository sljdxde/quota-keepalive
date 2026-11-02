import * as THREE from "./vendor/three.module.min.js";

function damp(current, target, lambda, delta) {
  return THREE.MathUtils.lerp(current, target, 1 - Math.exp(-lambda * delta));
}

function seededRandom(seed) {
  let state = seed >>> 0;
  return function random() {
    state = (state + 0x6d2b79f5) >>> 0;
    let value = state;
    value = Math.imul(value ^ (value >>> 15), value | 1);
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61);
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

function createFurTexture(base, light, dark, seed) {
  const size = 320;
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const context = canvas.getContext("2d");
  const random = seededRandom(seed);

  const wash = context.createLinearGradient(0, 0, size, size);
  wash.addColorStop(0, light);
  wash.addColorStop(0.5, base);
  wash.addColorStop(1, dark);
  context.fillStyle = wash;
  context.fillRect(0, 0, size, size);

  context.lineCap = "round";
  for (let index = 0; index < 2600; index++) {
    const x = random() * size;
    const y = random() * size;
    const length = 2 + random() * 9;
    const lean = (random() - 0.5) * 3;
    context.globalAlpha = 0.045 + random() * 0.12;
    context.strokeStyle = random() > 0.45 ? light : dark;
    context.lineWidth = 0.55 + random() * 0.8;
    context.beginPath();
    context.moveTo(x, y);
    context.quadraticCurveTo(x + lean, y + length * 0.45, x + lean * 1.5, y + length);
    context.stroke();
  }

  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  texture.wrapS = THREE.RepeatWrapping;
  texture.wrapT = THREE.RepeatWrapping;
  texture.repeat.set(1.8, 1.8);
  texture.anisotropy = 4;
  return texture;
}

function createContactShadow() {
  const canvas = document.createElement("canvas");
  canvas.width = 256;
  canvas.height = 128;
  const context = canvas.getContext("2d");
  const gradient = context.createRadialGradient(128, 64, 8, 128, 64, 118);
  gradient.addColorStop(0, "rgba(54, 23, 8, .34)");
  gradient.addColorStop(0.48, "rgba(54, 23, 8, .17)");
  gradient.addColorStop(1, "rgba(54, 23, 8, 0)");
  context.fillStyle = gradient;
  context.fillRect(0, 0, 256, 128);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

function furMaterial(texture, options) {
  return new THREE.MeshPhysicalMaterial(Object.assign({
    map: texture,
    bumpMap: texture,
    bumpScale: 0.022,
    roughness: 0.9,
    metalness: 0,
    sheen: 0.58,
    sheenColor: new THREE.Color(0xffc57c),
    sheenRoughness: 0.82,
    specularIntensity: 0.24,
  }, options || {}));
}

function addEllipsoid(parent, material, position, scale, segments) {
  const geometry = new THREE.SphereGeometry(1, segments || 48, (segments || 48) / 2);
  const mesh = new THREE.Mesh(geometry, material);
  mesh.position.set(position[0], position[1], position[2]);
  mesh.scale.set(scale[0], scale[1], scale[2]);
  parent.add(mesh);
  return mesh;
}

function addTube(parent, points, radius, material, segments) {
  const curve = new THREE.CatmullRomCurve3(
    points.map(function (point) {
      return new THREE.Vector3(point[0], point[1], point[2]);
    })
  );
  const geometry = new THREE.TubeGeometry(
    curve,
    segments || 44,
    radius,
    8,
    false
  );
  const mesh = new THREE.Mesh(geometry, material);
  parent.add(mesh);
  return mesh;
}

function addWhisker(parent, points, material) {
  const geometry = new THREE.BufferGeometry().setFromPoints(
    points.map(function (point) {
      return new THREE.Vector3(point[0], point[1], point[2]);
    })
  );
  const line = new THREE.Line(geometry, material);
  parent.add(line);
  return line;
}

function createTriangleGeometry(points) {
  const geometry = new THREE.BufferGeometry();
  geometry.setAttribute(
    "position",
    new THREE.Float32BufferAttribute(points.flat(), 3)
  );
  geometry.computeVertexNormals();
  return geometry;
}

function createEye(head, side, irisMaterial, pupilMaterial, shineMaterial) {
  const eye = new THREE.Group();
  eye.position.set(side * 0.255, 0.58, 0.69);
  head.add(eye);

  const sclera = addEllipsoid(
    eye,
    furMaterial(null, {
      color: 0xfff5dc,
      roughness: 0.38,
      sheen: 0,
    }),
    [0, 0, 0],
    [0.145, 0.132, 0.085],
    40
  );
  sclera.material.map = null;

  addEllipsoid(
    eye,
    irisMaterial,
    [0, -0.003, 0.077],
    [0.098, 0.102, 0.028],
    40
  );

  const pupil = addEllipsoid(
    eye,
    pupilMaterial,
    [0, 0, 0.105],
    [0.035, 0.078, 0.018],
    32
  );

  addEllipsoid(
    eye,
    shineMaterial,
    [side * -0.026, 0.046, 0.123],
    [0.017, 0.019, 0.009],
    20
  );
  addEllipsoid(
    eye,
    shineMaterial,
    [side * 0.025, -0.038, 0.121],
    [0.008, 0.009, 0.005],
    16
  );

  const lid = addTube(
    eye,
    [
      [-0.14, 0.055, 0.105],
      [-0.03, 0.112, 0.119],
      [0.14, 0.045, 0.105],
    ],
    0.011,
    new THREE.MeshStandardMaterial({
      color: 0xa94d18,
      roughness: 0.82,
    }),
    18
  );
  lid.position.z = 0.004;
  eye.scale.set(1.05, 0.84, 1);
  return { eye, pupil };
}

function createEar(head, side, outerMaterial, innerMaterial) {
  const ear = new THREE.Group();
  ear.position.set(side * 0.49, 0.99, 0.08);
  ear.rotation.z = side * -0.13;
  ear.rotation.x = -0.05;
  head.add(ear);

  const outer = new THREE.Mesh(
    new THREE.ConeGeometry(0.285, 0.5, 32, 4),
    outerMaterial
  );
  outer.scale.z = 0.74;
  ear.add(outer);

  const inner = new THREE.Mesh(
    createTriangleGeometry([
      [-0.115, -0.13, 0.18],
      [0.115, -0.13, 0.18],
      [0, 0.135, 0.16],
    ]),
    innerMaterial
  );
  ear.add(inner);
  return ear;
}

function createPaw(cat, side, fur, cream, dark) {
  const shoulder = new THREE.Group();
  shoulder.position.set(side * 0.34, -0.46, 0.44);
  shoulder.rotation.z = side * -0.035;
  cat.add(shoulder);

  const leg = new THREE.Mesh(
    new THREE.CapsuleGeometry(0.145, 0.52, 8, 22),
    fur
  );
  leg.position.set(0, -0.27, 0);
  shoulder.add(leg);

  addEllipsoid(
    shoulder,
    cream,
    [0, -0.57, 0.09],
    [0.18, 0.11, 0.23],
    32
  );

  const toeLine = addTube(
    shoulder,
    [
      [side * -0.055, -0.59, 0.285],
      [0, -0.61, 0.3],
      [side * 0.055, -0.59, 0.285],
    ],
    0.006,
    dark,
    12
  );
  toeLine.material.transparent = true;
  toeLine.material.opacity = 0.4;
  return shoulder;
}

function createCat(scene) {
  const furTexture = createFurTexture("#f39a3e", "#ffc16d", "#c75b1c", 31);
  const creamTexture = createFurTexture("#ffedc7", "#fff6e4", "#eab77c", 77);
  const fur = furMaterial(furTexture);
  const cream = furMaterial(creamTexture, {
    color: 0xfff1cf,
    sheenColor: new THREE.Color(0xffe2ad),
  });
  const stripeMaterial = new THREE.MeshStandardMaterial({
    color: 0xb64e16,
    roughness: 0.86,
    transparent: true,
    opacity: 0.62,
  });
  const pinkMaterial = new THREE.MeshPhysicalMaterial({
    color: 0xd86b72,
    roughness: 0.56,
    sheen: 0.24,
    sheenColor: new THREE.Color(0xffc1c5),
    side: THREE.DoubleSide,
  });
  const noseMaterial = new THREE.MeshPhysicalMaterial({
    color: 0xc96252,
    roughness: 0.42,
    clearcoat: 0.55,
    clearcoatRoughness: 0.2,
    side: THREE.DoubleSide,
  });
  const irisMaterial = new THREE.MeshPhysicalMaterial({
    color: 0x94b62f,
    roughness: 0.22,
    clearcoat: 1,
    clearcoatRoughness: 0.08,
    emissive: new THREE.Color(0x1f2804),
    emissiveIntensity: 0.11,
  });
  const pupilMaterial = new THREE.MeshPhysicalMaterial({
    color: 0x090706,
    roughness: 0.08,
    clearcoat: 1,
    clearcoatRoughness: 0.04,
  });
  const shineMaterial = new THREE.MeshBasicMaterial({ color: 0xfffdf4 });
  const darkMaterial = new THREE.MeshStandardMaterial({
    color: 0x7d3415,
    roughness: 0.82,
  });
  const whiskerMaterial = new THREE.LineBasicMaterial({
    color: 0xfff6dd,
    transparent: true,
    opacity: 0.85,
  });

  const root = new THREE.Group();
  root.position.set(0, -0.08, 0);
  scene.add(root);

  const body = new THREE.Group();
  root.add(body);

  addEllipsoid(body, fur, [0, -0.47, 0], [0.82, 0.93, 0.64], 64);
  addEllipsoid(body, cream, [0, -0.57, 0.51], [0.4, 0.64, 0.17], 52);
  addEllipsoid(body, fur, [-0.53, -0.69, 0.11], [0.44, 0.48, 0.5], 48);
  addEllipsoid(body, fur, [0.53, -0.69, 0.11], [0.44, 0.48, 0.5], 48);

  const leftPaw = createPaw(body, -1, fur, cream, darkMaterial);
  const rightPaw = createPaw(body, 1, fur, cream, darkMaterial);

  const tail = new THREE.Group();
  tail.position.set(0.56, -0.68, 0.03);
  body.add(tail);
  addTube(
    tail,
    [
      [0, 0, 0],
      [0.38, 0.11, 0.02],
      [0.7, 0.44, 0.07],
      [0.63, 0.82, 0.14],
    ],
    0.075,
    fur,
    52
  );
  addEllipsoid(tail, cream, [0.61, 0.85, 0.15], [0.082, 0.082, 0.082], 32);
  addTube(
    tail,
    [
      [0.33, 0.085, 0.018],
      [0.41, 0.17, 0.032],
    ],
    0.078,
    stripeMaterial,
    10
  );
  addTube(
    tail,
    [
      [0.54, 0.29, 0.052],
      [0.62, 0.41, 0.073],
    ],
    0.077,
    stripeMaterial,
    10
  );

  addTube(
    body,
    [
      [-0.62, -0.03, 0.42],
      [-0.42, 0.04, 0.6],
      [-0.16, 0.05, 0.66],
    ],
    0.018,
    stripeMaterial,
    24
  );
  addTube(
    body,
    [
      [-0.65, -0.2, 0.39],
      [-0.45, -0.14, 0.57],
      [-0.2, -0.12, 0.62],
    ],
    0.014,
    stripeMaterial,
    24
  );

  const headPivot = new THREE.Group();
  headPivot.position.set(0, 0.27, 0.12);
  root.add(headPivot);

  addEllipsoid(headPivot, fur, [0, 0.4, 0.03], [0.79, 0.7, 0.73], 64);
  addEllipsoid(headPivot, cream, [-0.135, 0.23, 0.65], [0.19, 0.14, 0.15], 36);
  addEllipsoid(headPivot, cream, [0.135, 0.23, 0.65], [0.19, 0.14, 0.15], 36);
  addEllipsoid(headPivot, cream, [0, 0.13, 0.61], [0.24, 0.12, 0.13], 34);
  addEllipsoid(headPivot, cream, [-0.3, 0.29, 0.56], [0.22, 0.17, 0.14], 34);
  addEllipsoid(headPivot, cream, [0.3, 0.29, 0.56], [0.22, 0.17, 0.14], 34);

  const leftEar = createEar(headPivot, -1, fur, pinkMaterial);
  const rightEar = createEar(headPivot, 1, fur, pinkMaterial);

  addTube(
    headPivot,
    [
      [-0.2, 0.95, 0.59],
      [-0.16, 0.76, 0.72],
      [-0.11, 0.61, 0.74],
    ],
    0.025,
    stripeMaterial,
    20
  );
  addTube(
    headPivot,
    [
      [0, 1.01, 0.59],
      [0, 0.79, 0.74],
      [0, 0.64, 0.76],
    ],
    0.028,
    stripeMaterial,
    20
  );
  addTube(
    headPivot,
    [
      [0.2, 0.95, 0.59],
      [0.16, 0.76, 0.72],
      [0.11, 0.61, 0.74],
    ],
    0.025,
    stripeMaterial,
    20
  );
  addTube(
    headPivot,
    [
      [-0.73, 0.48, 0.48],
      [-0.58, 0.47, 0.66],
      [-0.46, 0.45, 0.7],
    ],
    0.015,
    stripeMaterial,
    18
  );
  addTube(
    headPivot,
    [
      [0.73, 0.48, 0.48],
      [0.58, 0.47, 0.66],
      [0.46, 0.45, 0.7],
    ],
    0.015,
    stripeMaterial,
    18
  );

  const leftEye = createEye(headPivot, -1, irisMaterial, pupilMaterial, shineMaterial);
  const rightEye = createEye(headPivot, 1, irisMaterial, pupilMaterial, shineMaterial);

  const nose = new THREE.Mesh(
    createTriangleGeometry([
      [-0.052, 0.25, 0.83],
      [0.052, 0.25, 0.83],
      [0, 0.205, 0.86],
    ]),
    noseMaterial
  );
  headPivot.add(nose);

  const philtrum = addTube(
    headPivot,
    [
      [0, 0.21, 0.85],
      [0, 0.16, 0.86],
    ],
    0.009,
    darkMaterial,
    8
  );
  philtrum.material.transparent = true;
  philtrum.material.opacity = 0.65;
  addTube(
    headPivot,
    [
      [0, 0.16, 0.85],
      [-0.08, 0.12, 0.84],
      [-0.12, 0.145, 0.82],
    ],
    0.008,
    darkMaterial,
    18
  );
  addTube(
    headPivot,
    [
      [0, 0.16, 0.85],
      [0.08, 0.12, 0.84],
      [0.12, 0.145, 0.82],
    ],
    0.008,
    darkMaterial,
    18
  );

  [-1, 1].forEach(function (side) {
    for (let index = 0; index < 3; index++) {
      const y = 0.33 - index * 0.065;
      addWhisker(
        headPivot,
        [
          [side * 0.27, 0.26 + index * 0.02, 0.79],
          [side * (0.58 + index * 0.015), y + 0.015, 0.8],
          [side * (0.82 + index * 0.02), y + (index - 1) * 0.065, 0.74],
        ],
        whiskerMaterial
      );
    }
  });

  return {
    root,
    body,
    headPivot,
    leftPupil: leftEye.pupil,
    rightPupil: rightEye.pupil,
    leftEye: leftEye.eye,
    rightEye: rightEye.eye,
    leftEar,
    rightEar,
    leftPaw,
    rightPaw,
    tail,
  };
}

export function createCat3D(canvas, options) {
  if (!canvas) throw new Error("missing cat canvas");
  const settings = options || {};
  const renderer = new THREE.WebGLRenderer({
    canvas,
    alpha: true,
    antialias: true,
    powerPreference: "low-power",
  });
  renderer.setClearColor(0x000000, 0);
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.75));
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.06;

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(32, 1, 0.1, 30);
  camera.position.set(0, 0.35, 5.35);
  camera.lookAt(0, 0.03, 0);

  scene.add(new THREE.HemisphereLight(0xfff7e7, 0x6e3a1d, 2.7));
  const key = new THREE.DirectionalLight(0xffdfb3, 3.6);
  key.position.set(-3.4, 5.1, 4.7);
  scene.add(key);
  const fill = new THREE.DirectionalLight(0x9ec8ff, 2.15);
  fill.position.set(3.8, 1.5, 3.2);
  scene.add(fill);
  const rim = new THREE.DirectionalLight(0xffa451, 2.5);
  rim.position.set(-2.8, 1.8, -3.4);
  scene.add(rim);

  const cat = createCat(scene);
  const shadow = new THREE.Mesh(
    new THREE.PlaneGeometry(2.75, 0.92),
    new THREE.MeshBasicMaterial({
      map: createContactShadow(),
      transparent: true,
      depthWrite: false,
      opacity: 0.72,
    })
  );
  shadow.rotation.x = -Math.PI / 2;
  shadow.position.y = -1.46;
  scene.add(shadow);

  const reduceMotion = window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const pointerTarget = new THREE.Vector2();
  const pointerCurrent = new THREE.Vector2();
  const pupilTarget = new THREE.Vector2();
  const pupilCurrent = new THREE.Vector2();
  let enabled = false;
  let animationFrame = 0;
  let lastTime = performance.now();
  let elapsed = 0;
  let blinkAt = 2.4;
  let blinkStart = -1;
  let pounceStart = -1;
  let pounceX = 0;
  let pounceY = 0;
  let pointerEnergy = 0;
  let disposed = false;
  let resizeObserver = null;

  function renderOnce() {
    if (!disposed) renderer.render(scene, camera);
  }

  function resize() {
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(1, Math.round(rect.width));
    const height = Math.max(1, Math.round(rect.height));
    const pixelRatio = Math.min(window.devicePixelRatio || 1, 1.75);
    renderer.setPixelRatio(pixelRatio);
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    renderOnce();
  }

  function applyGaze(time, delta) {
    pointerCurrent.x = damp(pointerCurrent.x, pointerTarget.x, 10, delta);
    pointerCurrent.y = damp(pointerCurrent.y, pointerTarget.y, 10, delta);
    pupilCurrent.x = damp(pupilCurrent.x, pupilTarget.x, 12, delta);
    pupilCurrent.y = damp(pupilCurrent.y, pupilTarget.y, 12, delta);

    const pounceProgress = pounceStart < 0 ? 1 : Math.min(1, (time - pounceStart) / 640);
    const pouncing = pounceStart >= 0 && pounceProgress < 1;
    const lift = pouncing ? Math.sin(pounceProgress * Math.PI) : 0;
    const settle = pouncing ? Math.sin(pounceProgress * Math.PI * 2) : 0;

    if (pouncing) {
      pointerCurrent.x = damp(pointerCurrent.x, pounceX, 14, delta);
      pointerCurrent.y = damp(pointerCurrent.y, pounceY, 14, delta);
    } else if (pounceStart >= 0) {
      pounceStart = -1;
    }

    const breathing = reduceMotion ? 0 : Math.sin(elapsed * 1.55) * 0.006;
    cat.root.position.x = damp(cat.root.position.x, pointerCurrent.x * 0.105 + pounceX * lift * 0.09, 8, delta);
    cat.root.position.y = -0.08 + breathing + lift * 0.3;
    cat.root.rotation.x = -pointerCurrent.y * 0.025 - lift * 0.08;
    cat.root.rotation.y = pointerCurrent.x * 0.115;
    cat.root.rotation.z = -pointerCurrent.x * 0.028 + settle * pounceX * 0.035;
    cat.body.rotation.y = pointerCurrent.x * 0.085;
    cat.body.rotation.z = -pointerCurrent.x * 0.024;
    cat.body.scale.y = 1 + breathing * 0.4;

    cat.headPivot.rotation.y = damp(
      cat.headPivot.rotation.y,
      pointerCurrent.x * 0.54,
      9,
      delta
    );
    cat.headPivot.rotation.x = damp(
      cat.headPivot.rotation.x,
      -pointerCurrent.y * 0.25 - lift * 0.06,
      9,
      delta
    );
    cat.headPivot.rotation.z = damp(
      cat.headPivot.rotation.z,
      -pointerCurrent.x * 0.075 + Math.sin(elapsed * 1.2) * 0.008,
      8,
      delta
    );

    cat.leftPupil.position.x = -0.0 + pupilCurrent.x * 0.028;
    cat.rightPupil.position.x = pupilCurrent.x * 0.028;
    cat.leftPupil.position.y = pupilCurrent.y * -0.023;
    cat.rightPupil.position.y = pupilCurrent.y * -0.023;

    const earTwitch = reduceMotion ? 0 : Math.sin(elapsed * 2.2) * 0.012;
    cat.leftEar.rotation.z = 0.13 + earTwitch + pointerEnergy * 0.02;
    cat.rightEar.rotation.z = -0.13 - earTwitch - pointerEnergy * 0.02;

    const tailEnergy = pointerEnergy * 0.22;
    cat.tail.rotation.y = Math.sin(elapsed * 1.12) * 0.1 +
      pointerCurrent.x * 0.17 + tailEnergy * 0.08;
    cat.tail.rotation.z = Math.sin(elapsed * 1.48) * 0.07 -
      pointerCurrent.x * 0.13 - tailEnergy * 0.11;

    const pawLift = lift * 0.88;
    cat.leftPaw.rotation.x = -pawLift - pointerEnergy * 0.025;
    cat.rightPaw.rotation.x = -pawLift - pointerEnergy * 0.025;
    cat.leftPaw.rotation.z = -0.035 + lift * 0.06;
    cat.rightPaw.rotation.z = 0.035 - lift * 0.06;

    pointerEnergy = damp(pointerEnergy, 0, 5.2, delta);

    if (!reduceMotion) {
      if (blinkStart < 0 && elapsed >= blinkAt) blinkStart = elapsed;
      if (blinkStart >= 0) {
        const blinkProgress = (elapsed - blinkStart) / 0.16;
        const blinkScale = blinkProgress < 0.48
          ? 1 - blinkProgress / 0.48 * 0.91
          : 0.09 + (blinkProgress - 0.48) / 0.52 * 0.91;
        const scale = Math.max(0.08, Math.min(1, blinkScale));
        cat.leftEye.scale.y = scale;
        cat.rightEye.scale.y = scale;
        if (blinkProgress >= 1) {
          cat.leftEye.scale.y = 1;
          cat.rightEye.scale.y = 1;
          blinkStart = -1;
          blinkAt = elapsed + 2.8 + Math.random() * 2.6;
        }
      }
    }
  }

  function frame(now) {
    if (!enabled || disposed) {
      animationFrame = 0;
      return;
    }
    const delta = Math.min(0.05, Math.max(0.001, (now - lastTime) / 1000));
    lastTime = now;
    elapsed += delta;
    applyGaze(now, delta);
    renderOnce();
    animationFrame = requestAnimationFrame(frame);
  }

  function setEnabled(nextEnabled) {
    const shouldRun = !!nextEnabled;
    if (shouldRun === enabled) {
      if (shouldRun && !animationFrame) {
        lastTime = performance.now();
        animationFrame = requestAnimationFrame(frame);
      }
      return;
    }
    enabled = shouldRun;
    if (enabled) {
      lastTime = performance.now();
      if (!animationFrame) animationFrame = requestAnimationFrame(frame);
    } else if (animationFrame) {
      cancelAnimationFrame(animationFrame);
      animationFrame = 0;
    }
  }

  function setPointer(x, y) {
    const nextX = THREE.MathUtils.clamp(Number(x) || 0, -1, 1);
    const nextY = THREE.MathUtils.clamp(Number(y) || 0, -1, 1);
    pointerEnergy = Math.min(1, pointerEnergy +
      Math.abs(nextX - pointerTarget.x) * 1.8 +
      Math.abs(nextY - pointerTarget.y) * 1.2);
    pointerTarget.set(nextX, nextY);
    pupilTarget.set(nextX * 0.9, nextY * 0.9);
    if (reduceMotion) {
      pointerCurrent.copy(pointerTarget);
      pupilCurrent.copy(pupilTarget);
      applyGaze(performance.now(), 1);
      renderOnce();
    }
  }

  function resetPointer() {
    pointerTarget.set(0, 0);
    pupilTarget.set(0, 0);
    if (reduceMotion) {
      pointerCurrent.set(0, 0);
      pupilCurrent.set(0, 0);
      applyGaze(performance.now(), 1);
      renderOnce();
    }
  }

  function pounce(x, y) {
    pounceX = THREE.MathUtils.clamp(Number(x) || 0, -1, 1);
    pounceY = THREE.MathUtils.clamp(Number(y) || 0, -1, 1);
    if (reduceMotion) {
      pointerTarget.set(pounceX, pounceY);
      pupilTarget.set(pounceX, pounceY);
      setPointer(pounceX, pounceY);
      return;
    }
    pounceStart = performance.now();
    pointerEnergy = 1;
  }

  function dispose() {
    if (disposed) return;
    disposed = true;
    enabled = false;
    if (animationFrame) cancelAnimationFrame(animationFrame);
    animationFrame = 0;
    if (resizeObserver) resizeObserver.disconnect();
    scene.traverse(function (object) {
      if (object.geometry) object.geometry.dispose();
      if (!object.material) return;
      const materials = Array.isArray(object.material) ? object.material : [object.material];
      materials.forEach(function (material) {
        if (material.map) material.map.dispose();
        material.dispose();
      });
    });
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
    setEnabled(false);
    if (settings.onContextLost) settings.onContextLost();
  }, { once: true });
  renderOnce();

  return {
    setEnabled,
    setPointer,
    resetPointer,
    pounce,
    resize,
    dispose,
    get enabled() {
      return enabled;
    },
  };
}
