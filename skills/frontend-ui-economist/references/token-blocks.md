# Standalone Theme Token Blocks and Font Link

Reference for `skills/frontend-ui-economist/SKILL.md`. The same values ship with component CSS in `assets/theme-kit.css`; copy from there when the bundle is available, or paste a single theme block below when building standalone HTML.

```css
[data-theme="broadsheet"]{ color-scheme:light;
  --bg:#ffffff; --surface:#ffffff; --surface-2:#f3f3f0; --ink:#111111; --ink-2:#4a4a4a; --ink-3:#7a7a7a; --line:#d3d3ce; --line-2:#e8e8e3;
  --accent:#d62b1f; --accent-ink:#ffffff; --accent-soft:#fbe9e7; --accent-text:#d62b1f;
  --good:#1e7b46; --warn:#a5600f; --bad:#b3261e; --good-bg:#e6f3ec; --warn-bg:#fbefdc; --bad-bg:#fbe7e5; --neutral-bg:#efefeb;
  --s1:#1f5fa6; --s2:#d62b1f; --s3:#0f8f80; --s4:#b58a1c; --chart-tab:var(--accent);
  --font-display:"Fraunces",Georgia,"Times New Roman",serif; --font-text:"Source Serif 4",Georgia,serif; --font-ui:"Source Sans 3","Helvetica Neue",Arial,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:15px; --fs-sm:13px; --fs-xs:12px; --fs-h1:30px; --fs-h2:18px; --fs-head:42px; --fs-num:28px; --fs-prose:17px; --prose-lh:1.55;
  --radius:0px; --radius-s:0px; --pill-radius:0px; --btn-radius:0px; --input-radius:0px;
  --panel-bg:transparent; --panel-border:0; --panel-top:2px solid var(--ink); --panel-shadow:none; --table-top:2px solid var(--ink); --table-head-bg:transparent;
  --input-bg:#ffffff; --input-border:#8a8a8a; --dialog-shadow:0 0 0 1px var(--ink);
  --side-w:220px; --side-bg:#ffffff; --side-ink:#111111; --side-ink-2:#4a4a4a; --side-line:#d3d3ce;
  --nav-active-bg:transparent; --nav-active-ink:#111111; --nav-mark:var(--accent); --nav-mark-w:3px; --nav-radius:0px; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0px; --ws:1; }

[data-theme="ink"]{ color-scheme:dark;
  --bg:#0f1419; --surface:#161c23; --surface-2:#1d242d; --ink:#edebe6; --ink-2:#b3b0a9; --ink-3:#7f7c75; --line:#2c343e; --line-2:#222a33;
  --accent:#dc3328; --accent-ink:#ffffff; --accent-soft:#3a1d1b; --accent-text:#f0655b;
  --good:#4cc38a; --warn:#e3a92b; --bad:#ff6b61; --good-bg:rgba(76,195,138,.14); --warn-bg:rgba(227,169,43,.14); --bad-bg:rgba(255,107,97,.14); --neutral-bg:#232b35;
  --s1:#3f86d6; --s2:#e34b40; --s3:#2aa58f; --s4:#bd8526; --chart-tab:var(--accent);
  --font-display:"Newsreader",Georgia,serif; --font-text:"Newsreader",Georgia,serif; --font-ui:"Source Sans 3","Helvetica Neue",Arial,sans-serif;
  --display-weight:500; --display-tracking:-0.005em;
  --fs-base:15px; --fs-sm:13px; --fs-xs:12px; --fs-h1:30px; --fs-h2:18px; --fs-head:42px; --fs-num:28px; --fs-prose:17.5px; --prose-lh:1.6;
  --radius:0px; --radius-s:0px; --pill-radius:0px; --btn-radius:0px; --input-radius:0px;
  --panel-bg:var(--surface); --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:1px solid var(--ink-3); --table-head-bg:transparent;
  --input-bg:#121820; --input-border:#3a434e; --dialog-shadow:0 0 0 1px var(--ink-3); --bad-btn:#c73a2e;
  --side-w:220px; --side-bg:#0f1419; --side-ink:#edebe6; --side-ink-2:#b3b0a9; --side-line:#2c343e;
  --nav-active-bg:transparent; --nav-active-ink:#edebe6; --nav-mark:var(--accent); --nav-mark-w:3px; --nav-radius:0px; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0px; --ws:1; }

[data-theme="briefing"]{ color-scheme:light;
  --bg:#edf0f3; --surface:#ffffff; --surface-2:#f4f6f8; --ink:#0f1b2d; --ink-2:#45536a; --ink-3:#74819a; --line:#d3dae3; --line-2:#e6ebf0;
  --accent:#1d5a9a; --accent-ink:#ffffff; --accent-soft:#e2ecf7; --accent-text:#1d5a9a; --highlight:#d62b1f;
  --good:#187045; --warn:#8f5d06; --bad:#b8321f; --good-bg:#e4f2ea; --warn-bg:#faefd9; --bad-bg:#f9e5e1; --neutral-bg:#e9edf2;
  --s1:#1d5a9a; --s2:#d62b1f; --s3:#4c9bd6; --s4:#b38600; --chart-tab:var(--highlight);
  --font-display:"Source Serif 4",Georgia,serif; --font-text:"Source Serif 4",Georgia,serif; --font-ui:"IBM Plex Sans","Helvetica Neue",Arial,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:14px; --fs-sm:13px; --fs-xs:12px; --fs-h1:26px; --fs-h2:17px; --fs-head:36px; --fs-num:26px; --fs-prose:16.5px; --prose-lh:1.55;
  --radius:2px; --radius-s:2px; --pill-radius:2px; --btn-radius:2px; --input-radius:2px;
  --panel-bg:#ffffff; --panel-border:0; --panel-top:3px solid var(--accent); --panel-shadow:0 1px 2px rgba(15,27,45,.08); --table-top:1px solid var(--line); --table-head-bg:var(--surface-2);
  --input-bg:#ffffff; --input-border:#b7c2d0;
  --side-w:224px; --side-bg:#0f1b2d; --side-ink:#ffffff; --side-ink-2:#aab6c8; --side-line:#0f1b2d;
  --nav-active-bg:rgba(255,255,255,.08); --nav-active-ink:#ffffff; --nav-mark:var(--highlight); --nav-mark-w:3px; --nav-radius:0px; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:12px; --swatch-h:3px; --swatch-r:0px; --ws:1; }

[data-theme="airy"]{ color-scheme:light;
  --bg:#ffffff; --surface:#ffffff; --surface-2:#f1f3f4; --ink:#202124; --ink-2:#5f6368; --ink-3:#80868b; --line:#dadce0; --line-2:#e8eaed;
  --accent:#1a73e8; --accent-ink:#ffffff; --accent-soft:#e8f0fe; --accent-text:#1a73e8;
  --good:#188038; --warn:#b06000; --bad:#c5221f; --good-bg:#e6f4ea; --warn-bg:#fef7e0; --bad-bg:#fce8e6; --neutral-bg:#f1f3f4;
  --s1:#1a73e8; --s2:#e8710a; --s3:#9334e6; --s4:#0e9aa8; --chart-tab:var(--accent);
  --font-display:"Figtree",Roboto,system-ui,sans-serif; --font-text:"Figtree",Roboto,system-ui,sans-serif; --font-ui:"Figtree",Roboto,system-ui,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:14px; --fs-sm:13px; --fs-xs:12px; --fs-h1:28px; --fs-h2:16px; --fs-head:36px; --fs-num:32px; --fs-prose:15px; --prose-lh:1.65;
  --radius:12px; --radius-s:8px; --pill-radius:999px; --btn-radius:999px; --input-radius:8px;
  --panel-bg:#ffffff; --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:0; --table-head-bg:transparent;
  --input-bg:#ffffff; --input-border:#dadce0;
  --side-w:236px; --side-bg:#ffffff; --side-ink:#202124; --side-ink-2:#5f6368; --side-line:#ffffff;
  --nav-active-bg:#e8f0fe; --nav-active-ink:#1967d2; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:0 999px 999px 0; --nav-ml:calc(var(--u) * -1.5);
  --swatch-w:10px; --swatch-h:10px; --swatch-r:50%; --ws:1.4; }

[data-theme="instrument"]{ color-scheme:light;
  --bg:#f6f6f8; --surface:#ffffff; --surface-2:#f0f0f4; --ink:#15161a; --ink-2:#5a5d68; --ink-3:#8a8d98; --line:#e1e2e8; --line-2:#ebecf0;
  --accent:#4f46e5; --accent-ink:#ffffff; --accent-soft:#ecebfc; --accent-text:#4f46e5;
  --good:#15803d; --warn:#a16207; --bad:#d82222; --good-bg:#e7f6ec; --warn-bg:#fdf3dc; --bad-bg:#fdeaea; --neutral-bg:#ededf2;
  --s1:#4f46e5; --s2:#e5582a; --s3:#0d9488; --s4:#c026d3; --chart-tab:var(--accent);
  --font-display:"Manrope",system-ui,sans-serif; --font-text:"Manrope",system-ui,sans-serif; --font-ui:"Manrope",system-ui,sans-serif;
  --display-weight:700; --display-tracking:-0.015em;
  --fs-base:13px; --fs-sm:12px; --fs-xs:11px; --fs-h1:20px; --fs-h2:14px; --fs-head:26px; --fs-num:22px; --fs-prose:14px; --prose-lh:1.6;
  --radius:6px; --radius-s:4px; --pill-radius:4px; --btn-radius:5px; --input-radius:5px;
  --panel-bg:#ffffff; --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:0 1px 2px rgba(20,22,30,.04); --table-top:0; --table-head-bg:var(--surface-2);
  --input-bg:#ffffff; --input-border:#d6d7df;
  --side-w:200px; --side-bg:#f6f6f8; --side-ink:#15161a; --side-ink-2:#5a5d68; --side-line:#e1e2e8;
  --nav-active-bg:#e6e6ee; --nav-active-ink:#15161a; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:4px; --nav-ml:0px;
  --swatch-w:8px; --swatch-h:8px; --swatch-r:2px; --ws:0.8; }

[data-theme="graphite"]{ color-scheme:dark;
  --bg:#15181d; --surface:#1c2027; --surface-2:#242931; --ink:#e7e9ec; --ink-2:#9ba3ae; --ink-3:#6b7380; --line:#2c323b; --line-2:#262c34;
  --accent:#5563d8; --accent-ink:#ffffff; --accent-soft:rgba(85,99,216,.2); --accent-text:#8b9bff;
  --good:#3fb950; --warn:#d29922; --bad:#f85149; --good-bg:rgba(63,185,80,.15); --warn-bg:rgba(210,153,34,.15); --bad-bg:rgba(248,81,73,.15); --neutral-bg:#2a3039;
  --s1:#6b7ae8; --s2:#1aa88c; --s3:#d9671c; --s4:#c84fab; --chart-tab:var(--accent);
  --font-display:"DM Sans",system-ui,sans-serif; --font-text:"DM Sans",system-ui,sans-serif; --font-ui:"DM Sans",system-ui,sans-serif;
  --display-weight:600; --display-tracking:-0.01em;
  --fs-base:13.5px; --fs-sm:12.5px; --fs-xs:11.5px; --fs-h1:22px; --fs-h2:15px; --fs-head:28px; --fs-num:24px; --fs-prose:14.5px; --prose-lh:1.6;
  --radius:8px; --radius-s:6px; --pill-radius:999px; --btn-radius:6px; --input-radius:6px;
  --panel-bg:var(--surface); --panel-border:1px solid var(--line); --panel-top:1px solid var(--line); --panel-shadow:none; --table-top:0; --table-head-bg:transparent;
  --input-bg:#15181d; --input-border:#3a414c; --bad-btn:#c93434;
  --side-w:216px; --side-bg:#15181d; --side-ink:#e7e9ec; --side-ink-2:#9ba3ae; --side-line:#2c323b;
  --nav-active-bg:#242931; --nav-active-ink:#e7e9ec; --nav-mark:transparent; --nav-mark-w:0px; --nav-radius:6px; --nav-ml:0px;
  --swatch-w:10px; --swatch-h:10px; --swatch-r:50%; --ws:1; }
```

Google Fonts (load only the families your chosen theme uses):
`https://fonts.googleapis.com/css2?family=DM+Sans:opsz,wght@9..40,400;9..40,500;9..40,600&family=Figtree:wght@400;500;600;700&family=Fraunces:opsz,wght@9..144,500;9..144,600&family=IBM+Plex+Sans:wght@400;500;600&family=Manrope:wght@400;500;600;700&family=Newsreader:opsz,wght@6..72,400;6..72,500;6..72,600&family=Source+Sans+3:wght@400;500;600;700&family=Source+Serif+4:opsz,wght@8..60,400;8..60,600&display=swap`
