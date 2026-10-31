/* frontend-ui-economist / charts.js
   Small, dependency-free SVG charts that read colours from the theme tokens.
   Two styles:
     "editorial" - red/accent tab above the title, y labels on the RIGHT sitting on the gridline,
                   no y-axis line, baseline only, year ticks, series names labelled inside the plot
     "tool"      - y labels on the left, legend row above, values at the line ends
   Rules baked in (do not undo them): 2px lines, 8px end markers with a 2px surface ring, hairline solid
   gridlines, a legend for 2+ series, text in text tokens never in series colours, one axis only,
   a hover crosshair + tooltip, bars <= 24px with a 4px rounded data end and a square baseline.

   API
     Charts.sparkline(svgEl, values)                       // 12-point trend in a tile or a table cell
     Charts.line(containerEl, { labels, series, style, yMin, yMax, ticks, format, tickFormat, legendEl, label })
     Charts.column(containerEl, { labels, values, slot, valueName, style, yMax, ticks, format, tickFormat, label })
   series: [{ name: 'United States', slot: '--s1', values: [...] }]   (slot = a CSS token holding a colour)
*/
(function (global) {
  'use strict';
  const NS = 'http://www.w3.org/2000/svg';
  function mk(tag, attrs, parent) {
    const el = document.createElementNS(NS, tag);
    for (const k in attrs) el.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(el);
    return el;
  }
  function text(parent, x, y, str, attrs) {
    const t = mk('text', Object.assign({ x, y }, attrs || {}), parent);
    t.textContent = str;
    return t;
  }

  function sparkline(svg, values) {
    const W = svg.clientWidth || 80, H = svg.clientHeight || 28;
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`); svg.innerHTML = '';
    const min = Math.min(...values), max = Math.max(...values), span = (max - min) || 1;
    const x = i => 2 + i * (W - 6) / (values.length - 1), y = v => 3 + (H - 7) * (1 - (v - min) / span);
    const pts = values.map((v, i) => x(i) + ',' + y(v));
    mk('path', { d: 'M' + pts.join(' L') + ` L${x(values.length - 1)},${H - 1} L${x(0)},${H - 1} Z`, fill: 'var(--ink-3)', 'fill-opacity': '.1' }, svg);
    mk('path', { d: 'M' + pts.join(' L'), fill: 'none', stroke: 'var(--ink-3)', 'stroke-width': '1.5', 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }, svg);
    const lx = x(values.length - 1), ly = y(values[values.length - 1]);
    mk('circle', { cx: lx, cy: ly, r: 4, fill: 'var(--surface)' }, svg);   // surface ring
    mk('circle', { cx: lx, cy: ly, r: 2.5, fill: 'var(--accent)' }, svg);  // current period in the accent
  }

  function tooltip(container) {
    const tip = document.createElement('div'); tip.className = 'tip'; container.appendChild(tip); return tip;
  }

  function line(container, o) {
    const labels = o.labels, series = o.series, editorial = o.style === 'editorial';
    const fmt = o.format || (v => String(v)), tf = o.tickFormat || (v => String(v));
    container.innerHTML = '';
    const W = container.clientWidth || 600, H = container.clientHeight || 260;
    const m = editorial ? { t: 12, r: 30, b: 28, l: 4 } : { t: 12, r: 44, b: 28, l: 30 };
    const pw = W - m.l - m.r, ph = H - m.t - m.b;
    const yMin = o.yMin == null ? 0 : o.yMin, yMax = o.yMax, ticks = o.ticks;
    const n = labels.length;
    const x = i => m.l + i * pw / (n - 1), y = v => m.t + ph - (v - yMin) / (yMax - yMin) * ph;
    const svg = mk('svg', { viewBox: `0 0 ${W} ${H}`, width: W, height: H, role: 'img', 'aria-label': o.label || '' }, container);

    if (o.legendEl) o.legendEl.innerHTML = series.map(s => `<span><i style="--c:var(${s.slot})"></i>${s.name}</span>`).join('');

    ticks.forEach(t => {
      const yy = y(t);
      mk('line', { x1: m.l, x2: editorial ? W : m.l + pw, y1: yy, y2: yy, class: t === yMin ? 'base' : 'grid' }, svg);
      if (editorial) text(svg, W, yy - 4, tf(t), { 'text-anchor': 'end', class: 'ax' });
      else text(svg, m.l - 8, yy, tf(t), { 'text-anchor': 'end', 'dominant-baseline': 'middle', class: 'ax' });
    });

    // x labels: editorial marks period starts (labels[i].tick === true), tool spaces labels evenly
    if (editorial) {
      labels.forEach((lab, i) => {
        if (lab && lab.tick) {
          mk('line', { x1: x(i), x2: x(i), y1: y(yMin), y2: y(yMin) + 5, class: 'base' }, svg);
          text(svg, x(i) + 4, H - 8, lab.tick === true ? lab.text : lab.tick, { class: 'ax' });
        }
      });
    } else {
      const step = Math.max(1, Math.round((n - 1) / 4));
      const idx = []; for (let i = 0; i < n; i += step) idx.push(i); if (idx[idx.length - 1] !== n - 1) idx.push(n - 1);
      idx.forEach(i => text(svg, x(i), H - 8, labels[i].text || labels[i], { 'text-anchor': i === 0 ? 'start' : (i === n - 1 ? 'end' : 'middle'), class: 'ax' }));
    }

    const last = n - 1;
    series.forEach(s => {
      const d = s.values.map((v, i) => (i ? 'L' : 'M') + x(i).toFixed(1) + ',' + y(v).toFixed(1)).join(' ');
      mk('path', { d, fill: 'none', stroke: `var(${s.slot})`, 'stroke-width': 2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }, svg);
    });
    series.forEach(s => {
      mk('circle', { cx: x(last), cy: y(s.values[last]), r: 6, fill: 'var(--surface)' }, svg);
      mk('circle', { cx: x(last), cy: y(s.values[last]), r: 4, fill: `var(${s.slot})` }, svg);
    });

    if (editorial) {
      // name each series where it is farthest from the others; if nowhere has 16px of room, the legend carries it
      series.forEach((s, si) => {
        let best = -1, bestGap = 0;
        for (let i = 4; i <= n - 3; i++) {
          let gap = Infinity;
          series.forEach((o2, oi) => { if (oi !== si) gap = Math.min(gap, Math.abs(y(o2.values[i]) - y(s.values[i]))); });
          if (gap > bestGap + 0.5 || (gap >= bestGap - 0.5 && i > best)) { bestGap = gap; best = i; }
        }
        if (series.length > 1 && bestGap < 16) return;
        let above = true;
        series.forEach((o2, oi) => { if (oi !== si && Math.abs(y(o2.values[best]) - y(s.values[best])) === bestGap) above = y(o2.values[best]) > y(s.values[best]); });
        text(svg, x(best), y(s.values[best]) + (above ? -9 : 16), s.name, { 'text-anchor': 'middle', class: 'lbl' });
      });
    } else {
      const ends = series.map(s => ({ y: y(s.values[last]), t: fmt(s.values[last]) })).sort((a, b) => a.y - b.y);
      for (let i = 1; i < ends.length; i++) if (ends[i].y - ends[i - 1].y < 13) ends[i].y = ends[i - 1].y + 13;
      ends.forEach(l => text(svg, x(last) + 9, l.y, l.t, { 'dominant-baseline': 'middle', class: 'lbl' }));
    }

    // hover layer
    const xh = mk('line', { x1: 0, x2: 0, y1: m.t, y2: y(yMin), class: 'xh', style: 'display:none' }, svg);
    const hit = mk('rect', { x: m.l, y: m.t, width: pw, height: ph, class: 'hit' }, svg);
    const tip = tooltip(container);
    hit.addEventListener('pointermove', e => {
      const r = container.getBoundingClientRect(); const px = e.clientX - r.left;
      const i = Math.max(0, Math.min(n - 1, Math.round((px - m.l) / (pw / (n - 1)))));
      xh.setAttribute('x1', x(i)); xh.setAttribute('x2', x(i)); xh.style.display = '';
      tip.innerHTML = `<b>${labels[i].text || labels[i]}</b>` + series.map(s => `<div><i style="--c:var(${s.slot})"></i>${s.name}<span class="v">${fmt(s.values[i])}</span></div>`).join('');
      tip.style.display = 'block';
      const tw = tip.offsetWidth; let left = x(i) + 12; if (left + tw > W) left = x(i) - tw - 12;
      tip.style.left = left + 'px'; tip.style.top = (m.t + 4) + 'px';
    });
    hit.addEventListener('pointerleave', () => { xh.style.display = 'none'; tip.style.display = 'none'; });
  }

  function column(container, o) {
    const editorial = o.style === 'editorial', fmt = o.format || (v => String(v)), tf = o.tickFormat || (v => String(v));
    container.innerHTML = '';
    const W = container.clientWidth || 600, H = container.clientHeight || 220;
    const m = editorial ? { t: 14, r: 28, b: 30, l: 4 } : { t: 14, r: 12, b: 30, l: 30 };
    const pw = W - m.l - m.r, ph = H - m.t - m.b;
    const n = o.labels.length, slot = pw / n, bw = Math.min(24, slot * .55);
    const y = v => m.t + ph - (v / o.yMax) * ph;
    const svg = mk('svg', { viewBox: `0 0 ${W} ${H}`, width: W, height: H, role: 'img', 'aria-label': o.label || '' }, container);
    o.ticks.forEach(t => {
      const yy = y(t);
      mk('line', { x1: m.l, x2: editorial ? W : m.l + pw, y1: yy, y2: yy, class: t === 0 ? 'base' : 'grid' }, svg);
      if (editorial) text(svg, W, yy - 4, tf(t), { 'text-anchor': 'end', class: 'ax' });
      else text(svg, m.l - 8, yy, tf(t), { 'text-anchor': 'end', 'dominant-baseline': 'middle', class: 'ax' });
    });
    const tip = tooltip(container);
    o.labels.forEach((lab, i) => {
      const cx = m.l + slot * i + slot / 2, top = y(o.values[i]), base = y(0), h = base - top, r = Math.min(4, h);
      const d = `M${cx - bw / 2},${base} V${top + r} a${r},${r} 0 0 1 ${r},-${r} H${cx + bw / 2 - r} a${r},${r} 0 0 1 ${r},${r} V${base} Z`;
      const bar = mk('path', { d, fill: `var(${o.slot || '--s1'})` }, svg);
      text(svg, cx, top - 6, fmt(o.values[i]), { 'text-anchor': 'middle', class: 'lbl' });
      text(svg, cx, H - 10, lab, { 'text-anchor': 'middle', class: 'ax' });
      bar.addEventListener('pointerenter', () => {
        tip.innerHTML = `<b>${lab}</b><div>${o.valueName || ''}<span class="v">${fmt(o.values[i])}</span></div>`; tip.style.display = 'block';
        const tw = tip.offsetWidth; tip.style.left = Math.min(W - tw - 4, Math.max(0, cx - tw / 2)) + 'px'; tip.style.top = Math.max(0, top - 44) + 'px';
      });
      bar.addEventListener('pointerleave', () => { tip.style.display = 'none'; });
    });
  }

  global.Charts = { sparkline, line, column };
})(window);
