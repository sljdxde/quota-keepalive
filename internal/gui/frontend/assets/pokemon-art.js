// 北京时间当日累计 Token 只决定技能等级；伙伴形象始终使用首形态。
export const POKEMON_EVOLUTION_THRESHOLDS = [0, 200000000, 400000000];
export const POKEMON_MAX_DAILY_TOKENS = 1000000000;

const POKEMON_SOURCE = [
  {
    id: "bulbasaur",
    name: "妙蛙种子",
    stageNames: ["妙蛙种子", "妙蛙草", "妙蛙花"],
    stages: [
      { name: "妙蛙种子", sprite: "bulbasaur.png", model: "./assets/pokemon3d/1.glb" },
      { name: "妙蛙草", sprite: "ivysaur.png", model: "./assets/pokemon3d/2.glb" },
      { name: "妙蛙花", sprite: "venusaur.png", model: "./assets/pokemon3d/3.glb" },
    ],
    subtitle: "形象保持妙蛙种子；当日 Token 达到 2 亿、4 亿时依次强化藤鞭技能。",
    accent: "#63b978",
    effect: "leaf",
    messages: [
      "藤鞭正在校准鼠标位置。",
      "叶子上沾了一点今天的 Token。",
      "藤鞭蓄力完成，技能还会继续变强。",
    ],
  },
  {
    id: "charmander",
    name: "小火龙",
    stageNames: ["小火龙", "火恐龙", "喷火龙"],
    stages: [
      { name: "小火龙", sprite: "charmander.png", model: "./assets/pokemon3d/4.glb",
        // 模型绑定姿势是平举的 T-pose，肩部绕 Z 轴向下收一点，站姿立刻自然很多。
        pose: {
          LShoulder: [0, 0, -0.95],
          RShoulder: [0, 0, 0.95],
          LForeArm: [0, 0, -0.2],
          RForeArm: [0, 0, 0.2],
        } },
      { name: "火恐龙", sprite: "charmeleon.png", model: "./assets/pokemon3d/5.glb" },
      { name: "喷火龙", sprite: "charizard.png", model: "./assets/pokemon3d/6.glb" },
    ],
    subtitle: "形象保持小火龙；当日 Token 达到 2 亿、4 亿时依次强化火焰技能。",
    accent: "#ef8e43",
    effect: "ember",
    messages: [
      "火苗跟着点击跳了一下。",
      "今天的 Token，已经把尾巴烧起来了。",
      "火焰喷射已就绪，技能继续蓄力。",
    ],
  },
  {
    id: "squirtle",
    name: "杰尼龟",
    stageNames: ["杰尼龟", "卡咪龟", "水箭龟"],
    stages: [
      { name: "杰尼龟", sprite: "squirtle.png", model: "./assets/pokemon3d/7.glb" },
      { name: "卡咪龟", sprite: "wartortle.png", model: "./assets/pokemon3d/8.glb" },
      { name: "水箭龟", sprite: "blastoise.png", model: "./assets/pokemon3d/9.glb" },
    ],
    subtitle: "形象保持杰尼龟；当日 Token 达到 2 亿、4 亿时依次强化水泡技能。",
    accent: "#4ea9d7",
    effect: "water",
    messages: [
      "水泡已经瞄准鼠标。",
      "龟壳接住这一波今天的 Token。",
      "水炮充能完成，继续累积 Token。",
    ],
  },
  {
    id: "pikachu",
    name: "皮卡丘",
    stageNames: ["皮卡丘", "雷丘", "皮卡丘·巅峰"],
    stages: [
      { name: "皮卡丘", sprite: "pikachu.png", model: "./assets/pokemon3d/25.glb",
        rotation: [-Math.PI / 2, 0, 0] },
      { name: "雷丘", sprite: "raichu.png", model: "./assets/pokemon3d/26.glb" },
      { name: "皮卡丘·巅峰", sprite: "pikachu.png", model: "./assets/pokemon3d/25.glb",
        rotation: [-Math.PI / 2, 0, 0] },
    ],
    subtitle: "形象保持皮卡丘；当日 Token 越多，十万伏特技能越强。",
    accent: "#e4b51e",
    effect: "electric",
    messages: [
      "皮卡！鼠标位置收到。",
      "电弧已经跟上今天的 Token 总量。",
      "十万伏特的进度条正在发光。",
    ],
  },
];

function pokemonArtMarkup(pokemon) {
  const images = pokemon.stages.map(function (stage, index) {
    return `<img class="pokemon-stage-image" data-stage="${index + 1}" ` +
      `src="./assets/pokemon/${stage.sprite}" alt="" draggable="false">`;
  }).join("");
  return `<span class="pokemon-sprites" aria-hidden="true">${images}</span>`;
}

export const POKEMON_CATALOG = POKEMON_SOURCE.map(function (pokemon) {
  return Object.assign({}, pokemon, { art: pokemonArtMarkup(pokemon) });
});

export function pokemonById(id) {
  return POKEMON_CATALOG.find(function (pokemon) {
    return pokemon.id === id;
  }) || null;
}

export function evolutionStageForDailyTokens(todayTotalTokens) {
  const value = Math.max(0, Number(todayTotalTokens) || 0);
  let stage = 1;
  POKEMON_EVOLUTION_THRESHOLDS.forEach(function (threshold, index) {
    if (threshold > 0 && value >= threshold) stage = index + 1;
  });
  return stage;
}
