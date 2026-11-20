const rawDataNode = document.getElementById('graphData');
const DATA = JSON.parse(rawDataNode ? rawDataNode.textContent : '{}');
const PALETTE = (DATA.meta && DATA.meta.palette && DATA.meta.palette.length)
  ? DATA.meta.palette
  : ['#7aa2f7', '#9ece6a', '#bb9af7', '#ff9e64', '#7dcfff', '#f7768e', '#e0af68', '#73daca', '#f5a97f', '#c0caf5', '#a6e3a1', '#f38ba8'];
const LAYER_COLORS = (DATA.meta && DATA.meta.layerColors) || {};
const PACKAGES = DATA.packages || [];
const FILES = DATA.files || [];
const SYMBOLS = DATA.symbols || [];
const PKG_EDGES = DATA.pkgEdges || [];
const FILE_EDGES = DATA.fileEdges || [];
const SYM_EDGES = DATA.symEdges || [];
const HIDDEN_COUPLING = DATA.hiddenCoupling || [];
const PKG_CYCLES = (DATA.meta && DATA.meta.pkgCycles) || [];
const BRAIN_LINKS_MAP = DATA.brainLinks || {};

const canvas = document.getElementById('canvas');
const gctx = canvas.getContext('2d');

function makeEl(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function clearChildren(node) {
  while (node.firstChild) {
    node.removeChild(node.firstChild);
  }
}

function buildSearchPool() {
  const pool = [];
  PACKAGES.forEach((p, i) => {
    pool.push({ level: 'pkg', idx: i, title: p.id, sub: 'pkg · ' + (p.prodSymCount || p.symbolCount || 0) + ' syms', pr: (p.pr || 0) + 1 });
  });
  FILES.forEach((f, i) => {
    pool.push({ level: 'file', idx: i, title: f.id, sub: 'file · ' + (f.symbolCount || 0) + ' syms', pr: (f.pr || 0) + 0.2 });
  });
  SYMBOLS.forEach((s, i) => {
    const f = FILES[s.fileIdx];
    const pkgName = (f && f.pkg) || s.pkg || '';
    const fPath = (f && f.id) || s.file || '';
    pool.push({ level: 'sym', idx: i, title: (pkgName ? pkgName + '.' : '') + s.disp, sub: s.kind + ' · ' + fPath + ':' + s.start, pr: s.pr || 0 });
  });
  return pool;
}

function buildCycleLookups() {
  const nodes = new Set();
  const pairs = new Set();
  for (const comp of PKG_CYCLES) {
    const s = new Set(comp);
    comp.forEach((x) => nodes.add(x));
    for (const edge of PKG_EDGES) {
      if (edge[2] > 0 && s.has(edge[0]) && s.has(edge[1])) {
        pairs.add(edge[0] + ':' + edge[1]);
      }
    }
  }
  return { nodes, pairs };
}

const searchPool = buildSearchPool();
const cycleInfo = buildCycleLookups();
const cycleSet = cycleInfo.nodes;
const cyclePairs = cycleInfo.pairs;

const state = {
  level: 'pkg',
  prevLevel: 'pkg',
  includeTests: false,
  lang: 'all',
  colorBy: 'comm',
  edgeMask: 0,
  symCap: 400,
  labelMode: 'auto',
  showHulls: true,
  showCoupling: false,
  physics: true,
  light: false,
  filterComm: null,
  focus: null,
  selectedId: null,
  hoverId: null,
  camX: 0,
  camY: 0,
  camZ: 1,
  alpha: 0.45,
  dirty: true,
  nodes: [],
  edges: [],
  couplingArcs: [],
  origToLocal: new Map(),
  adjIn: [],
  adjOut: [],
  maxInDeg: 1,
  maxChurn: 1,
};

let rafHandle = 0;

function scheduleRender() {
  state.dirty = true;
  if (rafHandle === 0) {
    rafHandle = requestAnimationFrame(tickFrame);
  }
}

function wakeSimulation(minAlpha) {
  if (minAlpha !== undefined) {
    state.alpha = Math.max(state.alpha, minAlpha);
  }
  scheduleRender();
}

function tickFrame() {
  rafHandle = 0;
  const movedPhysics = stepPhysics();
  if (movedPhysics || state.dirty) {
    draw();
    state.dirty = false;
  }
  if (state.physics && state.alpha >= 0.0025 && state.nodes.length > 0) {
    rafHandle = requestAnimationFrame(tickFrame);
  }
}

function getLevelSource(level) {
  if (level === 'pkg') return { items: PACKAGES, edges: PKG_EDGES, radiusScale: 380 };
  if (level === 'file') return { items: FILES, edges: FILE_EDGES, radiusScale: 620 };
  return { items: SYMBOLS, edges: SYM_EDGES, radiusScale: 780 };
}

function resolveNodeLinks(level, raw) {
  const bl = raw.brainLinks;
  if (bl && ((bl.bugs || 0) + (bl.adrs || 0) + (bl.memories || 0) > 0 || (bl.ids && bl.ids.length))) {
    return bl;
  }
  if (level === 'file' && raw.id && BRAIN_LINKS_MAP[raw.id]) {
    return BRAIN_LINKS_MAP[raw.id];
  }
  if (level === 'sym') {
    const f = FILES[raw.fileIdx];
    if (f && f.brainLinks) return f.brainLinks;
    if (raw.file && BRAIN_LINKS_MAP[raw.file]) return BRAIN_LINKS_MAP[raw.file];
  }
  return { bugs: 0, adrs: 0, memories: 0, ids: [] };
}

function resolveNodeChurn(level, raw) {
  const src = (level === 'sym') ? FILES[raw.fileIdx] : raw;
  if (!src) return { clampHits: 0, repeatReads: 0, score: 0 };
  const clampHits = src.clampHits || 0;
  const repeatReads = src.repeatReads || 0;
  const score = src.churnScore !== undefined ? src.churnScore : (clampHits + repeatReads);
  return { clampHits, repeatReads, score };
}

function churnRampColor(score) {
  if (score <= 0) return state.light ? '#94a3b8' : '#475569';
  const ratio = score / Math.max(3, state.maxChurn);
  if (score <= 1 && ratio < 0.34) return state.light ? '#0284c7' : '#7dcfff';
  if (score <= 4 && ratio < 0.70) return state.light ? '#d97706' : '#e0af68';
  return state.light ? '#dc2626' : '#f7768e';
}

function recolorNodes() {
  for (const n of state.nodes) {
    if (state.colorBy === 'comm') {
      n.color = PALETTE[n.comm % PALETTE.length];
    } else if (state.colorBy === 'layer') {
      n.color = LAYER_COLORS[n.layer] || '#94a3b8';
    } else if (state.colorBy === 'churn') {
      n.color = churnRampColor(n.churnScore);
    } else {
      const t = Math.min(1, Math.sqrt(n.inDeg / Math.max(1, state.maxInDeg)));
      n.color = t < 0.35 ? '#64748b' : (t < 0.65 ? '#7dcfff' : (t < 0.85 ? '#e0af68' : '#f7768e'));
    }
  }
}

function viewportSize() {
  const w = canvas.clientWidth || window.innerWidth || 1280;
  const h = canvas.clientHeight || Math.max(400, (window.innerHeight || 840) - 48);
  return { w, h };
}

function fitCamera() {
  if (state.nodes.length === 0) {
    state.camX = 0;
    state.camY = 0;
    state.camZ = 1;
    scheduleRender();
    return;
  }
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of state.nodes) {
    if (n.x < minX) minX = n.x;
    if (n.x > maxX) maxX = n.x;
    if (n.y < minY) minY = n.y;
    if (n.y > maxY) maxY = n.y;
  }
  const { w, h } = viewportSize();
  const leftPad = document.getElementById('leftPanel').classList.contains('collapsed') ? 32 : 300;
  const rightPad = document.getElementById('rightPanel').classList.contains('open') ? 356 : 32;
  const availW = Math.max(260, w - leftPad - rightPad);
  const availH = Math.max(240, h - 80);
  const dx = Math.max(120, maxX - minX + 140);
  const dy = Math.max(120, maxY - minY + 140);
  state.camZ = Math.min(2.2, Math.max(0.18, 0.88 * Math.min(availW / dx, availH / dy)));
  const shiftWorldX = ((leftPad + availW / 2) - w / 2) / state.camZ;
  state.camX = (minX + maxX) / 2 - shiftWorldX;
  state.camY = (minY + maxY) / 2;
  scheduleRender();
}
