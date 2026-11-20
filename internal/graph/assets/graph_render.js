function convexHull(pts) {
  if (pts.length < 3) return pts;
  const s = pts.slice().sort((a, b) => (a[0] - b[0]) || (a[1] - b[1]));
  const cross = (o, a, b) => (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0]);
  const lower = [];
  for (const p of s) {
    while (lower.length >= 2 && cross(lower[lower.length - 2], lower[lower.length - 1], p) <= 0) lower.pop();
    lower.push(p);
  }
  const upper = [];
  for (let i = s.length - 1; i >= 0; i--) {
    const p = s[i];
    while (upper.length >= 2 && cross(upper[upper.length - 2], upper[upper.length - 1], p) <= 0) upper.pop();
    upper.push(p);
  }
  lower.pop();
  upper.pop();
  return lower.concat(upper);
}

function drawCommunityHulls() {
  if (!state.showHulls || state.nodes.length > 900) return;
  const byComm = new Map();
  for (const n of state.nodes) {
    if (!byComm.has(n.comm)) byComm.set(n.comm, []);
    byComm.get(n.comm).push([n.x, n.y]);
  }
  for (const [c, pts] of byComm.entries()) {
    if (pts.length < 3) continue;
    const hull = convexHull(pts);
    if (hull.length < 3) continue;
    const cx = pts.reduce((acc, p) => acc + p[0], 0) / pts.length;
    const cy = pts.reduce((acc, p) => acc + p[1], 0) / pts.length;
    gctx.beginPath();
    hull.forEach((p, idx) => {
      const dx = p[0] - cx, dy = p[1] - cy;
      const d = Math.hypot(dx, dy) || 1;
      const px = p[0] + (dx / d) * 28, py = p[1] + (dy / d) * 28;
      if (idx === 0) gctx.moveTo(px, py);
      else gctx.lineTo(px, py);
    });
    gctx.closePath();
    const col = PALETTE[c % PALETTE.length];
    gctx.fillStyle = col + (state.light ? '14' : '12');
    gctx.strokeStyle = col + (state.light ? '33' : '28');
    gctx.lineWidth = 1.2 / state.camZ;
    gctx.fill();
    gctx.stroke();
  }
}

function drawBezierArrowhead(nu, nv, mx, my, dist) {
  const t = Math.max(0.55, 1 - (nv.r + 5) / dist);
  const ax = (1 - t) * (1 - t) * nu.x + 2 * (1 - t) * t * mx + t * t * nv.x;
  const ay = (1 - t) * (1 - t) * nu.y + 2 * (1 - t) * t * my + t * t * nv.y;
  const tx = 2 * (1 - t) * (mx - nu.x) + 2 * t * (nv.x - mx);
  const ty = 2 * (1 - t) * (my - nu.y) + 2 * t * (nv.y - my);
  const ang = Math.atan2(ty, tx);
  const alen = 6.5 / Math.sqrt(state.camZ);
  gctx.fillStyle = gctx.strokeStyle;
  gctx.beginPath();
  gctx.moveTo(ax, ay);
  gctx.lineTo(ax - alen * Math.cos(ang - 0.42), ay - alen * Math.sin(ang - 0.42));
  gctx.lineTo(ax - alen * Math.cos(ang + 0.42), ay - alen * Math.sin(ang + 0.42));
  gctx.closePath();
  gctx.fill();
}

function drawCurvedEdges(activeIdx, inEdges, outEdges, colors) {
  for (const e of state.edges) {
    const nu = state.nodes[e.u], nv = state.nodes[e.v];
    const isIn = inEdges.has(e), isOut = outEdges.has(e);
    const highlighted = isIn || isOut;
    if (activeIdx !== undefined && !highlighted && !e.isCycle) {
      gctx.strokeStyle = colors.dim;
      gctx.lineWidth = 0.6 / state.camZ;
    } else if (isIn) {
      gctx.strokeStyle = colors.inc;
      gctx.lineWidth = (1.6 + Math.min(2.0, Math.log1p(e.w) * 0.45)) / state.camZ;
    } else if (isOut) {
      gctx.strokeStyle = colors.out;
      gctx.lineWidth = (1.6 + Math.min(2.0, Math.log1p(e.w) * 0.45)) / state.camZ;
    } else if (e.isCycle) {
      gctx.strokeStyle = colors.cyc + 'aa';
      gctx.lineWidth = 1.4 / state.camZ;
    } else {
      gctx.strokeStyle = colors.base;
      gctx.lineWidth = (0.7 + Math.min(1.5, Math.log1p(e.w) * 0.3)) / state.camZ;
    }
    const dx = nv.x - nu.x, dy = nv.y - nu.y;
    const dist = Math.hypot(dx, dy) || 1;
    const curve = Math.min(36, dist * 0.11);
    const mx = (nu.x + nv.x) / 2 + (-dy / dist) * curve;
    const my = (nu.y + nv.y) / 2 + (dx / dist) * curve;
    gctx.beginPath();
    gctx.moveTo(nu.x, nu.y);
    gctx.quadraticCurveTo(mx, my, nv.x, nv.y);
    gctx.stroke();
    if (highlighted || e.isCycle) drawBezierArrowhead(nu, nv, mx, my, dist);
  }
}

function drawCouplingArcs(activeIdx, colors) {
  if (!state.showCoupling || state.couplingArcs.length === 0) return;
  gctx.save();
  gctx.setLineDash([6 / state.camZ, 4 / state.camZ]);
  for (const arc of state.couplingArcs) {
    const nu = state.nodes[arc.u], nv = state.nodes[arc.v];
    if (!nu || !nv) continue;
    const focusArc = activeIdx === undefined || activeIdx === arc.u || activeIdx === arc.v;
    gctx.strokeStyle = focusArc ? colors.coup : colors.dim;
    gctx.lineWidth = (1.3 + Math.min(2.0, Math.log1p(arc.count) * 0.4)) / state.camZ;
    const dx = nv.x - nu.x, dy = nv.y - nu.y;
    const dist = Math.hypot(dx, dy) || 1;
    const mx = (nu.x + nv.x) / 2 + (dy / dist) * Math.min(48, dist * 0.16);
    const my = (nu.y + nv.y) / 2 - (dx / dist) * Math.min(48, dist * 0.16);
    gctx.beginPath();
    gctx.moveTo(nu.x, nu.y);
    gctx.quadraticCurveTo(mx, my, nv.x, nv.y);
    gctx.stroke();
  }
  gctx.restore();
}

function drawNodeDots(activeIdx, neighborSet, cycCol) {
  const sortedByPr = state.nodes.slice().sort((a, b) => b.pr - a.pr);
  const godCutoff = sortedByPr.length ? sortedByPr[Math.min(9, sortedByPr.length - 1)].pr : 1;
  for (let i = 0; i < state.nodes.length; i++) {
    const n = state.nodes[i];
    const dimmed = activeIdx !== undefined && !neighborSet.has(i);
    const isSelected = state.selectedId === n.origIdx;
    const isHovered = state.hoverId === n.origIdx;
    const isGod = n.pr >= godCutoff && n.pr > 0;
    const hasBugs = n.brainLinks && n.brainLinks.bugs > 0;
    gctx.globalAlpha = dimmed ? 0.2 : 1.0;
    if ((isGod || isSelected || isHovered) && !dimmed) {
      gctx.beginPath();
      gctx.arc(n.x, n.y, n.r + (isSelected || isHovered ? 5 : 3.5), 0, Math.PI * 2);
      gctx.fillStyle = n.color + '33';
      gctx.fill();
    }
    gctx.beginPath();
    gctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
    gctx.fillStyle = n.color;
    gctx.fill();
    gctx.lineWidth = (isSelected || isHovered ? 2.4 : 1.1) / Math.max(0.5, state.camZ);
    gctx.strokeStyle = (isSelected || isHovered) ? (state.light ? '#0f172a' : '#ffffff') : (state.light ? '#ffffff' : '#0b0f17');
    gctx.stroke();
    if ((state.level === 'pkg' && cycleSet.has(n.origIdx)) || hasBugs) {
      gctx.beginPath();
      gctx.arc(n.x, n.y, n.r + 2.5, 0, Math.PI * 2);
      gctx.strokeStyle = cycCol;
      gctx.lineWidth = 1.3 / Math.max(0.5, state.camZ);
      gctx.stroke();
    }
  }
  gctx.globalAlpha = 1.0;
}

function drawSmartLabels(activeIdx, neighborSet) {
  if (state.labelMode === 'off' && activeIdx === undefined) return;
  const fontSize = Math.max(9, Math.min(13, 11 / Math.pow(state.camZ, 0.35)));
  gctx.font = '500 ' + fontSize + 'px ui-sans-serif, system-ui, sans-serif';
  gctx.textBaseline = 'middle';
  const cands = [];
  for (let i = 0; i < state.nodes.length; i++) {
    const n = state.nodes[i];
    const autoVis = state.labelMode === 'auto' && activeIdx === undefined &&
      (n.r * state.camZ >= 7.5 || (state.level === 'pkg' && n.r * state.camZ >= 4.5));
    if (activeIdx === i || neighborSet.has(i) || (state.labelMode === 'all' && activeIdx === undefined) || autoVis) {
      cands.push({ i, n, priority: activeIdx === i ? 999 : (neighborSet.has(i) ? 500 + n.pr : n.pr) });
    }
  }
  cands.sort((a, b) => b.priority - a.priority);
  const maxLabels = state.labelMode === 'all' ? 400 : (activeIdx !== undefined ? 45 : 75);
  const placedBoxes = [];
  for (const cand of cands.slice(0, maxLabels)) {
    placeSingleLabel(cand.i, cand.n, fontSize, activeIdx, neighborSet, placedBoxes);
  }
}

function placeSingleLabel(i, n, fontSize, activeIdx, neighborSet, placedBoxes) {
  const lx = n.x + n.r + 4 / state.camZ, ly = n.y;
  const tw = gctx.measureText(n.label).width, th = fontSize + 3;
  const box = [lx - 2, ly - th / 2, lx + tw + 4, ly + th / 2];
  if (activeIdx !== i && !neighborSet.has(i) && state.labelMode !== 'all') {
    for (const b of placedBoxes) {
      if (box[0] < b[2] && box[2] > b[0] && box[1] < b[3] && box[3] > b[1]) return;
    }
  }
  placedBoxes.push(box);
  gctx.fillStyle = state.light ? 'rgba(248,250,252,0.84)' : 'rgba(11,15,23,0.82)';
  gctx.fillRect(box[0], box[1], tw + 5, th);
  gctx.fillStyle = state.light ? '#0f172a' : '#e2e8f0';
  gctx.fillText(n.label, lx, ly);
}

function draw() {
  const dpr = window.devicePixelRatio || 1;
  const { w, h } = viewportSize();
  const targetW = Math.max(1, Math.floor(w * dpr)), targetH = Math.max(1, Math.floor(h * dpr));
  if (canvas.width !== targetW || canvas.height !== targetH) {
    canvas.width = targetW;
    canvas.height = targetH;
  }
  gctx.save();
  gctx.scale(dpr, dpr);
  gctx.fillStyle = state.light ? '#f8fafc' : '#0b0f17';
  gctx.fillRect(0, 0, w, h);
  gctx.translate(w / 2, h / 2);
  gctx.scale(state.camZ, state.camZ);
  gctx.translate(-state.camX, -state.camY);

  const activeIdx = state.hoverId !== null
    ? state.origToLocal.get(state.hoverId)
    : (state.selectedId !== null ? state.origToLocal.get(state.selectedId) : undefined);
  const neighborSet = new Set(), inEdges = new Set(), outEdges = new Set();
  if (activeIdx !== undefined) {
    neighborSet.add(activeIdx);
    for (const e of state.adjIn[activeIdx]) { neighborSet.add(e.u); inEdges.add(e); }
    for (const e of state.adjOut[activeIdx]) { neighborSet.add(e.v); outEdges.add(e); }
  }
  const colors = {
    base: state.light ? 'rgba(71, 85, 105, 0.20)' : 'rgba(148, 163, 184, 0.18)',
    dim: state.light ? 'rgba(71, 85, 105, 0.05)' : 'rgba(148, 163, 184, 0.04)',
    inc: state.light ? '#0284c7' : '#7dcfff',
    out: state.light ? '#d97706' : '#ff9e64',
    cyc: state.light ? '#dc2626' : '#f7768e',
    coup: state.light ? '#7c3aed' : '#bb9af7',
  };
  drawCommunityHulls();
  drawCurvedEdges(activeIdx, inEdges, outEdges, colors);
  drawCouplingArcs(activeIdx, colors);
  drawNodeDots(activeIdx, neighborSet, colors.cyc);
  drawSmartLabels(activeIdx, neighborSet);
  gctx.restore();
}
