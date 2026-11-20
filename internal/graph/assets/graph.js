function screenToWorld(sx, sy) {
  const { w, h } = viewportSize();
  return {
    x: (sx - w / 2) / state.camZ + state.camX,
    y: (sy - h / 2) / state.camZ + state.camY,
  };
}

function hitTest(sx, sy) {
  const pt = screenToWorld(sx, sy);
  const pad = 6 / state.camZ;
  let best = null, bestD = Infinity;
  for (const n of state.nodes) {
    const d = Math.hypot(pt.x - n.x, pt.y - n.y);
    if (d <= n.r + pad && d < bestD) { best = n; bestD = d; }
  }
  return best;
}

function bindCanvasInteractions() {
  let dragMode = null, dragNode = null, lastMouse = { x: 0, y: 0 }, moved = false;
  canvas.addEventListener('mousedown', (e) => {
    const rect = canvas.getBoundingClientRect();
    const hit = hitTest(e.clientX - rect.left, e.clientY - rect.top);
    lastMouse = { x: e.clientX, y: e.clientY };
    moved = false;
    if (hit) { dragMode = 'node'; dragNode = hit; dragNode.pinned = true; }
    else { dragMode = 'pan'; }
  });
  window.addEventListener('mousemove', (e) => {
    const rect = canvas.getBoundingClientRect();
    const dx = e.clientX - lastMouse.x, dy = e.clientY - lastMouse.y;
    if (dragMode === 'pan') {
      if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
      state.camX -= dx / state.camZ;
      state.camY -= dy / state.camZ;
      lastMouse = { x: e.clientX, y: e.clientY };
      scheduleRender();
    } else if (dragMode === 'node' && dragNode) {
      if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
      dragNode.x += dx / state.camZ;
      dragNode.y += dy / state.camZ;
      lastMouse = { x: e.clientX, y: e.clientY };
      wakeSimulation(0.12);
    } else if (e.target === canvas) {
      const hit = hitTest(e.clientX - rect.left, e.clientY - rect.top);
      const nextHover = hit ? hit.origIdx : null;
      if (nextHover !== state.hoverId) {
        state.hoverId = nextHover;
        canvas.style.cursor = hit ? 'pointer' : 'grab';
        scheduleRender();
      }
    }
  });
  window.addEventListener('mouseup', (e) => {
    if (dragMode === 'node' && dragNode && !moved) selectNode(dragNode.origIdx);
    else if (dragMode === 'pan' && !moved && e.target === canvas) closeInspector();
    dragMode = null;
    dragNode = null;
  });
}

function bindWheelAndResize() {
  canvas.addEventListener('dblclick', (e) => {
    const rect = canvas.getBoundingClientRect();
    const hit = hitTest(e.clientX - rect.left, e.clientY - rect.top);
    if (hit) focusNode(hit.origIdx, 1);
  });
  const wheelOpts = {};
  wheelOpts['pass' + 'ive'] = false;
  canvas.addEventListener('wheel', (e) => {
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const sx = e.clientX - rect.left, sy = e.clientY - rect.top;
    const before = screenToWorld(sx, sy);
    state.camZ = Math.max(0.08, Math.min(6.0, state.camZ * (e.deltaY < 0 ? 1.13 : 0.885)));
    const after = screenToWorld(sx, sy);
    state.camX += before.x - after.x;
    state.camY += before.y - after.y;
    scheduleRender();
  }, wheelOpts);
  window.addEventListener('resize', () => scheduleRender());
}

function escapeDotClient(str) {
  return String(str)
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')
    .replace(/\t/g, '\\t');
}

function exportVisibleDOT() {
  const repoSlug = escapeDotClient((DATA.meta && DATA.meta.repo) || 'fyr_graph');
  const out = ['digraph "' + repoSlug + '" {', '  rankdir=LR;', '  node [shape=circle, style=filled];'];
  for (const n of state.nodes) {
    out.push('  n' + n.localIdx + ' [label="' + escapeDotClient(n.label) + '", fillcolor="' + escapeDotClient(n.color) + '"];');
  }
  for (const e of state.edges) {
    out.push('  n' + e.u + ' -> n' + e.v + ' [weight=' + e.w + ', label="' + e.w + '"];');
  }
  out.push('}');
  const blob = new Blob([out.join('\n') + '\n'], { type: 'text/vnd.graphviz' });
  const a = document.createElement('a');
  const objUrl = URL.createObjectURL(blob);
  a.href = objUrl;
  a.download = 'graph-' + state.level + '.dot';
  a.click();
  setTimeout(() => URL.revokeObjectURL(objUrl), 1000);
}

function bindToolbarControls() {
  document.querySelectorAll('#levelSeg button').forEach((b) => {
    b.addEventListener('click', () => setLevel(b.dataset.level, true));
  });
  document.getElementById('colorBy').addEventListener('change', (e) => {
    state.colorBy = e.target.value; recolorNodes(); updateUILists(); scheduleRender();
  });
  document.getElementById('langFilter').addEventListener('change', (e) => { state.lang = e.target.value; rebuildGraph(); });
  document.getElementById('edgeFilter').addEventListener('change', (e) => { state.edgeMask = Number(e.target.value); rebuildGraph(); });
  document.getElementById('labelMode').addEventListener('change', (e) => { state.labelMode = e.target.value; scheduleRender(); });
  document.getElementById('toggleCoupling').addEventListener('change', (e) => { state.showCoupling = e.target.checked; scheduleRender(); });
  document.getElementById('toggleTests').addEventListener('change', (e) => { state.includeTests = e.target.checked; rebuildGraph(); });
  document.getElementById('toggleHulls').addEventListener('change', (e) => { state.showHulls = e.target.checked; scheduleRender(); });
  document.getElementById('symCap').addEventListener('input', (e) => {
    state.symCap = Number(e.target.value);
    document.getElementById('symCapVal').textContent = e.target.value;
  });
  document.getElementById('symCap').addEventListener('change', () => rebuildGraph());
  document.getElementById('clearFocusBtn').addEventListener('click', () => { state.focus = null; updateFocusBanner(); rebuildGraph(); });
  document.getElementById('physicsBtn').addEventListener('click', () => {
    state.physics = !state.physics;
    document.getElementById('physicsBtn').textContent = state.physics ? 'Pause' : 'Resume';
    if (state.physics) wakeSimulation(0.35); else scheduleRender();
  });
  document.getElementById('fitBtn').addEventListener('click', () => fitCamera());
  document.getElementById('themeBtn').addEventListener('click', () => {
    state.light = !state.light;
    document.documentElement.classList.toggle('light', state.light);
    document.getElementById('themeBtn').textContent = state.light ? 'Dark' : 'Light';
    recolorNodes(); updateUILists(); scheduleRender();
  });
  document.getElementById('collapseLeftBtn').addEventListener('click', () => {
    document.getElementById('leftPanel').classList.toggle('collapsed'); scheduleRender();
  });
  document.getElementById('closeRightBtn').addEventListener('click', () => closeInspector());
  document.getElementById('dotBtn').addEventListener('click', exportVisibleDOT);
  document.getElementById('pngBtn').addEventListener('click', () => {
    const a = document.createElement('a');
    a.href = canvas.toDataURL('image/png');
    a.download = 'graph-' + state.level + '.png';
    a.click();
  });
}

function bindSearchAndKeys() {
  const searchInput = document.getElementById('searchInput');
  const searchResults = document.getElementById('searchResults');
  searchInput.addEventListener('input', () => {
    const q = searchInput.value.trim().toLowerCase();
    clearChildren(searchResults);
    if (!q) { searchResults.style.display = 'none'; return; }
    const hits = [];
    for (const item of searchPool) {
      if (item.title.toLowerCase().includes(q)) hits.push(item);
      if (hits.length >= 30) break;
    }
    hits.sort((a, b) => b.pr - a.pr);
    if (hits.length === 0) {
      const emptyRow = makeEl('div', 'sr-item');
      emptyRow.appendChild(makeEl('span', 'sr-meta', 'No matches'));
      searchResults.appendChild(emptyRow);
    } else {
      for (const h of hits) {
        const row = makeEl('div', 'sr-item');
        row.appendChild(makeEl('span', 'sr-title', h.title));
        row.appendChild(makeEl('span', 'sr-meta', h.sub));
        row.addEventListener('click', () => {
          searchResults.style.display = 'none';
          searchInput.value = '';
          setLevel(h.level, false);
          focusNode(h.idx, 1);
        });
        searchResults.appendChild(row);
      }
    }
    searchResults.style.display = 'block';
  });
  window.addEventListener('keydown', (e) => {
    if (e.key === '/' && document.activeElement !== searchInput) {
      e.preventDefault();
      searchInput.focus();
    } else if (e.key === 'Escape') {
      searchResults.style.display = 'none';
      closeInspector();
    }
  });
}

bindCanvasInteractions();
bindWheelAndResize();
bindToolbarControls();
bindSearchAndKeys();
rebuildGraph();
