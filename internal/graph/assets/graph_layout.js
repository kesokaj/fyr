function computeSpiralFallback(nodes, radiusScale) {
  if (nodes.length === 0) return;
  const groups = new Map();
  for (let i = 0; i < nodes.length; i++) {
    const c = nodes[i].comm || 0;
    if (!groups.has(c)) groups.set(c, []);
    groups.get(c).push(i);
  }
  const commKeys = Array.from(groups.keys()).sort((a, b) => a - b);
  const k = Math.max(1, commKeys.length);
  const ringR = radiusScale * (0.65 + 0.04 * Math.min(k, 12));
  const golden = Math.PI * (3.0 - Math.sqrt(5.0));
  commKeys.forEach((c, ci) => {
    const ang = (2 * Math.PI * ci) / k - Math.PI / 2;
    const cx = k > 1 ? ringR * Math.cos(ang) : 0;
    const cy = k > 1 ? ringR * Math.sin(ang) : 0;
    placeGroupSpiralNodes(nodes, groups.get(c), c, cx, cy, golden);
  });
}

function placeGroupSpiralNodes(nodes, rawMembers, comm, cx, cy, golden) {
  const members = rawMembers.slice().sort((a, b) => (nodes[b].pr - nodes[a].pr) || (a - b));
  const m = members.length;
  const spread = 28 + 18 * Math.sqrt(m);
  members.forEach((localIdx, rankIdx) => {
    const nd = nodes[localIdx];
    if (nd.hasPos) return;
    if (rankIdx === 0 && m > 1) {
      nd.x = cx;
      nd.y = cy;
      return;
    }
    const r = spread * Math.sqrt((rankIdx + 0.5) / Math.max(1, m));
    const theta = rankIdx * golden + comm * 0.7;
    nd.x = cx + r * Math.cos(theta);
    nd.y = cy + r * Math.sin(theta);
  });
}

function buildCouplingArcs() {
  state.couplingArcs = [];
  if (HIDDEN_COUPLING.length === 0) return;
  if (state.level === 'file') {
    for (const tup of HIDDEN_COUPLING) {
      const u = state.origToLocal.get(tup[0]);
      const v = state.origToLocal.get(tup[1]);
      if (u !== undefined && v !== undefined && u !== v) {
        state.couplingArcs.push({ u, v, count: tup[2] || 1, kind: tup[3] || 'co_activity' });
      }
    }
    return;
  }
  if (state.level === 'pkg') {
    buildPkgCouplingArcs();
  }
}

function buildPkgCouplingArcs() {
  const pairMap = new Map();
  for (const tup of HIDDEN_COUPLING) {
    const fa = FILES[tup[0]];
    const fb = FILES[tup[1]];
    if (!fa || !fb || fa.pkgIdx === fb.pkgIdx) continue;
    const u = state.origToLocal.get(fa.pkgIdx);
    const v = state.origToLocal.get(fb.pkgIdx);
    if (u === undefined || v === undefined || u === v) continue;
    const a = Math.min(u, v), b = Math.max(u, v);
    const key = a + ':' + b;
    const prev = pairMap.get(key);
    if (prev) prev.count += (tup[2] || 1);
    else pairMap.set(key, { u: a, v: b, count: tup[2] || 1, kind: tup[3] || 'co_activity' });
  }
  state.couplingArcs = Array.from(pairMap.values());
}

function collectFocusedOrigins(items, edges) {
  const adj = new Map();
  for (const edge of edges) {
    const w = state.includeTests ? (edge[2] + edge[3]) : edge[2];
    if (w <= 0 || (state.edgeMask && !(edge[4] & state.edgeMask))) continue;
    if (!adj.has(edge[0])) adj.set(edge[0], []);
    if (!adj.has(edge[1])) adj.set(edge[1], []);
    adj.get(edge[0]).push(edge[1]);
    adj.get(edge[1]).push(edge[0]);
  }
  const keep = new Set();
  let frontier = new Set();
  for (const r of state.focus.rootIndices) {
    if (r >= 0 && r < items.length) { keep.add(r); frontier.add(r); }
  }
  for (let h = 0; h < state.focus.hops; h++) {
    const next = new Set();
    for (const u of frontier) {
      for (const v of (adj.get(u) || [])) {
        if (items[v] && (state.includeTests || !items[v].isTest) && !keep.has(v)) {
          keep.add(v);
          next.add(v);
        }
      }
    }
    frontier = next;
  }
  return Array.from(keep);
}

function collectFilteredOrigins(items) {
  const cands = [];
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (!state.includeTests && it.isTest) continue;
    if (state.lang !== 'all' && it.lang !== state.lang) continue;
    if (state.filterComm !== null && it.comm !== state.filterComm) continue;
    cands.push(i);
  }
  if (state.level === 'sym' && cands.length > state.symCap) {
    cands.sort((a, b) => (items[b].pr - items[a].pr) || ((items[b].inDeg || 0) - (items[a].inDeg || 0)));
    cands.length = state.symCap;
  }
  return cands;
}

function createSimNode(origIdx, localIdx, it, maxPr, prev, totalCount) {
  let label = it.label || it.disp || it.name || String(origIdx);
  let fullLabel = it.id || it.qualName || label;
  if (state.level === 'file' && it.id) {
    const parts = it.id.split('/');
    label = parts.length >= 2 ? parts.slice(-2).join('/') : it.id;
  } else if (state.level === 'sym') {
    const f = FILES[it.fileIdx];
    label = it.disp || it.name;
    const pkgPrefix = (f && f.pkg) || it.pkg || '';
    fullLabel = pkgPrefix ? (pkgPrefix + '.' + label) : label;
  }
  const baseR = state.level === 'pkg' ? 7 : (state.level === 'file' ? 4.5 : 4);
  const spanR = state.level === 'pkg' ? 18 : (state.level === 'file' ? 11 : 10);
  const hasCoord = prev || (typeof it.x === 'number' && typeof it.y === 'number' && (it.x !== 0 || it.y !== 0 || totalCount === 1));
  const churn = resolveNodeChurn(state.level, it);
  return {
    origIdx, localIdx, label, fullLabel,
    pr: it.pr || 0, comm: it.comm || 0, layer: it.layer || 'other', lang: it.lang || 'go',
    isTest: !!it.isTest, isDoc: !!it.isDoc,
    inDeg: state.includeTests ? (it.inDeg || 0) : (it.prodInDeg !== undefined ? it.prodInDeg : (it.inDeg || 0)),
    outDeg: state.includeTests ? (it.outDeg || 0) : (it.prodOutDeg !== undefined ? it.prodOutDeg : (it.outDeg || 0)),
    clampHits: churn.clampHits, repeatReads: churn.repeatReads, churnScore: churn.score,
    brainLinks: resolveNodeLinks(state.level, it),
    r: baseR + spanR * Math.sqrt(Math.max(0, it.pr || 0) / maxPr),
    x: prev ? prev.x : (it.x || 0), y: prev ? prev.y : (it.y || 0),
    hasPos: !!hasCoord, vx: 0, vy: 0, pinned: false,
    color: PALETTE[(it.comm || 0) % PALETTE.length],
  };
}

function buildLocalEdges(edges) {
  state.edges = [];
  state.adjIn = Array.from({ length: state.nodes.length }, () => []);
  state.adjOut = Array.from({ length: state.nodes.length }, () => []);
  for (const edge of edges) {
    const u = state.origToLocal.get(edge[0]);
    const v = state.origToLocal.get(edge[1]);
    const w = state.includeTests ? (edge[2] + edge[3]) : edge[2];
    if (u === undefined || v === undefined || u === v || w <= 0) continue;
    if (state.edgeMask && !(edge[4] & state.edgeMask)) continue;
    const e = { u, v, w, mask: edge[4], isCycle: state.level === 'pkg' && cyclePairs.has(edge[0] + ':' + edge[1]) };
    state.edges.push(e);
    state.adjOut[u].push(e);
    state.adjIn[v].push(e);
  }
}

function rebuildGraph() {
  const { items, edges, radiusScale } = getLevelSource(state.level);
  const origList = (state.focus && state.focus.level === state.level)
    ? collectFocusedOrigins(items, edges)
    : collectFilteredOrigins(items);
  const prevPos = new Map();
  if (state.prevLevel === state.level) {
    for (const n of state.nodes) prevPos.set(n.origIdx, { x: n.x, y: n.y });
  }
  state.prevLevel = state.level;
  const maxPr = origList.reduce((m, i) => Math.max(m, items[i].pr || 0), 1e-6);
  state.nodes = [];
  state.origToLocal = new Map();
  for (const origIdx of origList) {
    const localIdx = state.nodes.length;
    state.origToLocal.set(origIdx, localIdx);
    state.nodes.push(createSimNode(origIdx, localIdx, items[origIdx], maxPr, prevPos.get(origIdx), origList.length));
  }
  computeSpiralFallback(state.nodes, radiusScale);
  buildLocalEdges(edges);
  buildCouplingArcs();
  state.maxInDeg = Math.max(1, ...state.nodes.map((n) => n.inDeg));
  state.maxChurn = Math.max(1, ...state.nodes.map((n) => n.churnScore));
  recolorNodes();
  state.alpha = 0.45;
  const warmSteps = state.nodes.length <= 200 ? 45 : (state.nodes.length <= 600 ? 22 : 10);
  const prevPhysics = state.physics;
  state.physics = true;
  for (let s = 0; s < warmSteps; s++) stepPhysics();
  state.physics = prevPhysics;
  updateUILists();
  fitCamera();
  scheduleRender();
}

function repelDirect(nodes, repStrength, a) {
  for (let i = 0; i < nodes.length; i++) {
    const ni = nodes[i];
    for (let j = i + 1; j < nodes.length; j++) {
      const nj = nodes[j];
      const dx = ni.x - nj.x, dy = ni.y - nj.y;
      const d2 = dx * dx + dy * dy + 0.01;
      if (d2 > 220000) continue;
      const d = Math.sqrt(d2);
      const minD = ni.r + nj.r + 12;
      const mag = Math.min(18, ((repStrength * a) / d2) * 18) + (d < minD ? (minD - d) * 0.25 : 0);
      const ux = (dx / d) * mag, uy = (dy / d) * mag;
      if (!ni.pinned) { ni.vx += ux; ni.vy += uy; }
      if (!nj.pinned) { nj.vx -= ux; nj.vy -= uy; }
    }
  }
}

function repelSpatialGrid(nodes, repStrength, a) {
  const cellSize = 110;
  const grid = new Map();
  for (let i = 0; i < nodes.length; i++) {
    const gk = Math.floor(nodes[i].x / cellSize) + ':' + Math.floor(nodes[i].y / cellSize);
    if (!grid.has(gk)) grid.set(gk, []);
    grid.get(gk).push(i);
  }
  for (let i = 0; i < nodes.length; i++) {
    repelGridNeighbors(nodes, i, grid, cellSize, repStrength, a);
  }
}

function repelGridNeighbors(nodes, i, grid, cellSize, repStrength, a) {
  const ni = nodes[i];
  const gx = Math.floor(ni.x / cellSize), gy = Math.floor(ni.y / cellSize);
  for (let ox = -2; ox <= 2; ox++) {
    for (let oy = -2; oy <= 2; oy++) {
      const bucket = grid.get((gx + ox) + ':' + (gy + oy));
      if (!bucket) continue;
      for (const j of bucket) {
        if (j <= i) continue;
        const nj = nodes[j];
        const dx = ni.x - nj.x, dy = ni.y - nj.y;
        const d2 = dx * dx + dy * dy + 0.01;
        if (d2 > cellSize * cellSize * 4) continue;
        const d = Math.sqrt(d2);
        const minD = ni.r + nj.r + 7;
        const mag = Math.min(14, ((repStrength * a) / d2) * 14) + (d < minD ? (minD - d) * 0.22 : 0);
        const ux = (dx / d) * mag, uy = (dy / d) * mag;
        if (!ni.pinned) { ni.vx += ux; ni.vy += uy; }
        if (!nj.pinned) { nj.vx -= ux; nj.vy -= uy; }
      }
    }
  }
}

function applySpringsAndGravity(nodes, a) {
  const idealLen = state.level === 'pkg' ? 140 : 90;
  for (const e of state.edges) {
    const nu = nodes[e.u], nv = nodes[e.v];
    const dx = nv.x - nu.x, dy = nv.y - nu.y;
    const d = Math.sqrt(dx * dx + dy * dy) || 1;
    const pull = (d - idealLen) * 0.014 * a * (1 + Math.min(2, Math.log1p(e.w) * 0.35));
    const fx = (dx / d) * pull, fy = (dy / d) * pull;
    if (!nu.pinned) { nu.vx += fx; nu.vy += fy; }
    if (!nv.pinned) { nv.vx -= fx; nv.vy -= fy; }
  }
  const commSum = new Map();
  for (const nd of nodes) {
    let c = commSum.get(nd.comm);
    if (!c) { c = { x: 0, y: 0, k: 0 }; commSum.set(nd.comm, c); }
    c.x += nd.x; c.y += nd.y; c.k++;
  }
  for (const nd of nodes) {
    if (nd.pinned) continue;
    const c = commSum.get(nd.comm);
    nd.vx = (nd.vx + ((c.x / c.k) - nd.x) * 0.015 * a - nd.x * 0.004 * a) * 0.78;
    nd.vy = (nd.vy + ((c.y / c.k) - nd.y) * 0.015 * a - nd.y * 0.004 * a) * 0.78;
    nd.x += Math.max(-24, Math.min(24, nd.vx));
    nd.y += Math.max(-24, Math.min(24, nd.vy));
  }
}

function stepPhysics() {
  if (!state.physics || state.alpha < 0.0025 || state.nodes.length === 0) return false;
  const repStrength = state.level === 'pkg' ? 2400 : (state.level === 'file' ? 950 : 750);
  if (state.nodes.length <= 320) repelDirect(state.nodes, repStrength, state.alpha);
  else repelSpatialGrid(state.nodes, repStrength, state.alpha);
  applySpringsAndGravity(state.nodes, state.alpha);
  state.alpha *= 0.988;
  return true;
}
