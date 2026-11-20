function makeStatCard(label, val) {
  const card = makeEl('div', 'stat');
  card.appendChild(makeEl('div', 'k', label));
  card.appendChild(makeEl('div', 'v', val));
  return card;
}

function makeNeighborRow(targetNode, weightText) {
  const row = makeEl('div', 'list-item');
  const sw = makeEl('span', 'dot-swatch');
  sw.style.background = targetNode.color;
  const lbl = makeEl('span', 'mono truncate', targetNode.label);
  lbl.style.flex = '1';
  lbl.title = targetNode.fullLabel;
  row.appendChild(sw);
  row.appendChild(lbl);
  row.appendChild(makeEl('span', 'badge', weightText));
  row.addEventListener('click', () => {
    const ln = state.nodes[state.origToLocal.get(targetNode.origIdx)];
    if (ln) {
      state.camX = ln.x;
      state.camY = ln.y;
      selectNode(targetNode.origIdx);
    }
  });
  return row;
}

function appendBrainLinksSection(container, links) {
  const ids = (links && links.ids) ? links.ids : [];
  const total = (links ? (links.bugs || 0) + (links.adrs || 0) + (links.memories || 0) : 0) || ids.length;
  container.appendChild(makeEl('div', 'sec-title', 'Brain Record Links (' + total + ')'));
  if (total === 0) {
    container.appendChild(makeEl('div', 'dim-note', '0 linked records in brain.db'));
    return;
  }
  const summaryRow = makeEl('div', 'btn-row');
  if (links.bugs) summaryRow.appendChild(makeEl('span', 'badge bug', 'bugs: ' + links.bugs));
  if (links.adrs) summaryRow.appendChild(makeEl('span', 'badge adr', 'adrs: ' + links.adrs));
  if (links.memories) summaryRow.appendChild(makeEl('span', 'badge mem', 'memories: ' + links.memories));
  container.appendChild(summaryRow);
  const idWrap = makeEl('div', 'btn-row');
  for (const id of ids) {
    let cls = 'badge mono';
    if (id.startsWith('BUG-')) cls += ' bug';
    else if (id.startsWith('ADR-')) cls += ' adr';
    else if (id.startsWith('MEMORY-')) cls += ' mem';
    idWrap.appendChild(makeEl('span', cls, id));
  }
  container.appendChild(idWrap);
}

function appendCouplingSection(container, localIdx) {
  const peers = [];
  for (const arc of state.couplingArcs) {
    if (arc.u === localIdx) peers.push({ peer: state.nodes[arc.v], count: arc.count, kind: arc.kind });
    else if (arc.v === localIdx) peers.push({ peer: state.nodes[arc.u], count: arc.count, kind: arc.kind });
  }
  if (peers.length === 0) return;
  peers.sort((a, b) => b.count - a.count);
  container.appendChild(makeEl('div', 'sec-title', 'Hidden Co-Activity (' + peers.length + ')'));
  for (const p of peers.slice(0, 15)) {
    if (p.peer) container.appendChild(makeNeighborRow(p.peer, p.count + ' · ' + p.kind));
  }
}

function appendInspectorSubtitle(body, n, raw) {
  if (state.level === 'sym') {
    const f = FILES[raw.fileIdx];
    const fPath = (f && f.id) || raw.file || '';
    body.appendChild(makeEl('div', 'dim-note', fPath + ':' + raw.start + '-' + raw.end));
    if (raw.sig) body.appendChild(makeEl('pre', 'sig mono', raw.sig));
    return;
  }
  if (state.level === 'pkg') {
    const pFiles = raw.prodFileCount !== undefined ? raw.prodFileCount : (raw.fileCount || 0);
    const syms = raw.prodSymCount !== undefined ? raw.prodSymCount : (raw.symbolCount || 0);
    body.appendChild(makeEl('div', 'dim-note', pFiles + ' prod files (' + (raw.testFileCount || 0) + ' test) · ' + syms + ' symbols · ' + (raw.lineCount || 0) + ' lines'));
    return;
  }
  body.appendChild(makeEl('div', 'dim-note', 'pkg: ' + (raw.pkg || '.') + ' · ' + (raw.symbolCount || 0) + ' symbols · ' + (raw.lineCount || 0) + ' lines'));
}

function appendInspectorActions(body, origIdx) {
  const actions = makeEl('div', 'btn-row');
  const b1 = makeEl('button', 'btn primary', 'Focus 1-hop');
  b1.type = 'button';
  b1.addEventListener('click', () => focusNode(origIdx, 1));
  actions.appendChild(b1);
  const b2 = makeEl('button', 'btn', 'Focus 2-hop');
  b2.type = 'button';
  b2.addEventListener('click', () => focusNode(origIdx, 2));
  actions.appendChild(b2);
  if (state.level === 'pkg') {
    const bf = makeEl('button', 'btn', 'Files in pkg');
    bf.type = 'button';
    bf.addEventListener('click', () => drillPkgFiles(origIdx));
    actions.appendChild(bf);
  } else if (state.level === 'file') {
    const bs = makeEl('button', 'btn', 'Symbols in file');
    bs.type = 'button';
    bs.addEventListener('click', () => drillFileSyms(origIdx));
    actions.appendChild(bs);
  }
  body.appendChild(actions);
}

function appendEdgeSection(body, heading, list, pickPeer) {
  body.appendChild(makeEl('div', 'sec-title', heading + ' (' + list.length + ')'));
  if (list.length === 0) {
    body.appendChild(makeEl('div', 'dim-note', 'None in current view'));
    return;
  }
  for (const e of list.slice(0, 40)) {
    body.appendChild(makeNeighborRow(pickPeer(e), String(e.w)));
  }
}

function selectNode(origIdx) {
  state.selectedId = origIdx;
  const localIdx = state.origToLocal.get(origIdx);
  if (localIdx === undefined) { scheduleRender(); return; }
  const n = state.nodes[localIdx];
  const raw = getLevelSource(state.level).items[origIdx];
  const body = document.getElementById('inspBody');
  clearChildren(body);
  document.getElementById('inspKind').textContent =
    state.level === 'pkg' ? 'Package' : (state.level === 'file' ? 'File' : ('Symbol (' + raw.kind + ')'));
  const titleEl = makeEl('div', 'mono', n.fullLabel);
  titleEl.style.cssText = 'font-size:14px;font-weight:700;word-break:break-all;';
  body.appendChild(titleEl);
  const badgeRow = makeEl('div', 'btn-row');
  badgeRow.appendChild(makeEl('span', 'badge', 'layer: ' + n.layer));
  badgeRow.appendChild(makeEl('span', 'badge', 'lang: ' + n.lang));
  badgeRow.appendChild(makeEl('span', 'badge', 'C' + n.comm));
  if (n.isTest) badgeRow.appendChild(makeEl('span', 'badge', 'test'));
  if (n.isDoc) badgeRow.appendChild(makeEl('span', 'badge', 'doc'));
  body.appendChild(badgeRow);
  appendInspectorSubtitle(body, n, raw);
  const inc = state.adjIn[localIdx].slice().sort((a, b) => b.w - a.w);
  const out = state.adjOut[localIdx].slice().sort((a, b) => b.w - a.w);
  const grid = makeEl('div', 'stats-grid');
  grid.style.marginTop = '8px';
  grid.appendChild(makeStatCard('PageRank', n.pr.toFixed(5)));
  grid.appendChild(makeStatCard('In / Out', inc.length + ' / ' + out.length));
  grid.appendChild(makeStatCard('Read Churn', String(n.churnScore)));
  grid.appendChild(makeStatCard('Clamp / Repeat', n.clampHits + ' / ' + n.repeatReads));
  body.appendChild(grid);
  appendInspectorActions(body, origIdx);
  appendBrainLinksSection(body, n.brainLinks);
  appendCouplingSection(body, localIdx);
  appendEdgeSection(body, 'Incoming / Callers', inc, (e) => state.nodes[e.u]);
  appendEdgeSection(body, 'Outgoing / Callees', out, (e) => state.nodes[e.v]);
  document.getElementById('rightPanel').classList.add('open');
  scheduleRender();
}

function closeInspector() {
  state.selectedId = null;
  document.getElementById('rightPanel').classList.remove('open');
  scheduleRender();
}

function focusNode(origIdx, hops) {
  const it = getLevelSource(state.level).items[origIdx];
  if (!it) return;
  const lbl = it.id || it.disp || it.name || String(origIdx);
  state.focus = { level: state.level, rootIndices: new Set([origIdx]), hops, label: lbl + ' (' + hops + '-hop)' };
  updateFocusBanner();
  rebuildGraph();
  selectNode(origIdx);
}

function drillPkgFiles(pkgIdx) {
  const roots = new Set();
  FILES.forEach((f, i) => {
    if (f.pkgIdx === pkgIdx && (state.includeTests || !f.isTest)) roots.add(i);
  });
  if (roots.size === 0) return;
  setLevel('file', false);
  state.focus = { level: 'file', rootIndices: roots, hops: 1, label: 'Files in ' + PACKAGES[pkgIdx].id };
  updateFocusBanner();
  rebuildGraph();
}

function drillFileSyms(fileIdx) {
  const roots = new Set();
  SYMBOLS.forEach((s, i) => { if (s.fileIdx === fileIdx) roots.add(i); });
  if (roots.size === 0) return;
  setLevel('sym', false);
  state.focus = { level: 'sym', rootIndices: roots, hops: 1, label: 'Symbols in ' + FILES[fileIdx].id };
  updateFocusBanner();
  rebuildGraph();
}

function updateFocusBanner() {
  const b = document.getElementById('focusBanner');
  if (state.focus) {
    document.getElementById('focusLabel').textContent = state.focus.label;
    b.classList.add('show');
  } else {
    b.classList.remove('show');
  }
}

function renderSidebarGods() {
  const godList = document.getElementById('godList');
  clearChildren(godList);
  const gods = state.nodes.slice().sort((a, b) => (b.pr - a.pr) || (b.inDeg - a.inDeg)).slice(0, 10);
  for (const n of gods) {
    godList.appendChild(makeNeighborRow(n, n.inDeg + '↓'));
  }
}

function renderSidebarComms() {
  const commCounts = new Map();
  for (const n of state.nodes) commCounts.set(n.comm, (commCounts.get(n.comm) || 0) + 1);
  const commLegend = document.getElementById('commLegend');
  clearChildren(commLegend);
  for (const [c, cnt] of Array.from(commCounts.entries()).sort((a, b) => a[0] - b[0])) {
    const chip = makeEl('button', 'comm-chip' + (state.filterComm === c ? ' active' : ''));
    chip.type = 'button';
    const sw = makeEl('span', 'dot-swatch');
    sw.style.background = PALETTE[c % PALETTE.length];
    chip.appendChild(sw);
    chip.appendChild(document.createTextNode('C' + c + ' (' + cnt + ')'));
    chip.addEventListener('click', () => {
      state.filterComm = state.filterComm === c ? null : c;
      rebuildGraph();
    });
    commLegend.appendChild(chip);
  }
}

function renderSidebarCycles() {
  const cycList = document.getElementById('cyclesList');
  clearChildren(cycList);
  if (PKG_CYCLES.length === 0) {
    cycList.appendChild(makeEl('div', 'dim-note', '0 production package cycles'));
    return;
  }
  PKG_CYCLES.forEach((comp) => {
    const row = makeEl('div', 'list-item');
    const sw = makeEl('span', 'dot-swatch');
    sw.style.background = 'var(--cycle-color)';
    const lbl = makeEl('span', 'mono truncate', comp.map((idx) => (PACKAGES[idx] ? PACKAGES[idx].id : String(idx))).join(' ↔ '));
    lbl.style.flex = '1';
    row.appendChild(sw);
    row.appendChild(lbl);
    row.appendChild(makeEl('span', 'badge', String(comp.length)));
    row.addEventListener('click', () => {
      setLevel('pkg', false);
      state.focus = { level: 'pkg', rootIndices: new Set(comp), hops: 1, label: 'Cycle (' + comp.length + ' pkgs)' };
      updateFocusBanner();
      rebuildGraph();
    });
    cycList.appendChild(row);
  });
}

function updateUILists() {
  document.getElementById('statNodes').textContent = String(state.nodes.length);
  document.getElementById('statEdges').textContent = String(state.edges.length);
  document.getElementById('statComms').textContent = String(new Set(state.nodes.map((n) => n.comm)).size);
  document.getElementById('statCycles').textContent = String(PKG_CYCLES.length);
  renderSidebarGods();
  renderSidebarComms();
  renderSidebarCycles();
}

function setLevel(lvl, doRebuild = true) {
  state.level = lvl;
  state.focus = null;
  state.selectedId = null;
  state.filterComm = null;
  updateFocusBanner();
  closeInspector();
  document.querySelectorAll('#levelSeg button').forEach((b) => {
    b.classList.toggle('active', b.dataset.level === lvl);
  });
  document.getElementById('symCapWrap').style.display = lvl === 'sym' ? 'inline-flex' : 'none';
  if (doRebuild) rebuildGraph();
}
