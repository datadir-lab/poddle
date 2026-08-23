// Hand-built animated SVG: a 3-scene poddle demo in a brand frame.
//   1 shell : up --detach, attach, env|grep ANTHROPIC (broker+handle), claude
//   2 TUI   : a close reconstruction of the Claude Code interface — welcome box,
//             the input box present from launch, the prompt typed into it and
//             submitted, streaming tool calls, and a live poddle BLOCK of an
//             off-policy fetch; input box + "? for shortcuts" at the bottom
//   3 audit : poddled audit (held) — allow / redact / deny / allow, intact chain
// Native monospace <text> + real rounded-rect boxes. Broker facts are faithful
// to the harness; the Claude Code panel is a stylized reconstruction.
//   node gen-svg.js out.svg [atSeconds]
const fs = require('fs');
const OUT = process.argv[2];
const AT = process.argv[3] !== undefined ? parseFloat(process.argv[3]) : null;

const FS = 15, LH = 25, CW = 9, PADX = 30, BAR = 50, asc = 12, W = 884, TOP = BAR + 18;
const H = 610;
const P = {
  ink: '#14130d', fg: '#d3cfc3', bright: '#f4f0e7', dim: '#847f71', faint: '#5f5b50',
  green: '#63c08c', amber: '#e3b371', red: '#e0736b', cyan: '#6cc6d8',
  claude: '#e0955e', dot: '#63b98a', boxln: 'rgba(244,240,231,.15)',
  bone: '#faf8f2', mark: '#2f9e6f',
};
const S = (t, c) => ({ t, c: c || P.fg });
const rowY = (r) => TOP + r * LH + asc;
const cp = (str, w) => str + ' '.repeat(Math.max(0, w - [...str].length));
const esc = (x) => x.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const tsp = (segs) => segs.map(g => `<tspan fill="${g.c}">${esc(g.t)}</tspan>`).join('');
const vlen = (segs) => segs.reduce((n, g) => n + [...g.t].length, 0);

let css = `text{font-family:"SFMono-Regular","SF Mono",Menlo,Consolas,"Liberation Mono","DejaVu Sans Mono",monospace;font-size:${FS}px;white-space:pre}.wm{font-family:Georgia,"Times New Roman",serif}`;
let uid = 0, D = 1;
const ops = [];
const txt = (x, y, segs, anim) => ops.push({ kind: 'text', x, y, segs, anim });
const boxAt = (y, h, t) => ops.push({ kind: 'box', x: 14, y, w: W - 28, h, anim: { mode: 'reveal', t, end: 0 } });
const cmdLine = (r, prompt, cmd, t) => { txt(PADX, rowY(r), [...prompt, S(cmd, P.bright)], { mode: 'reveal', t, end: 0 }); return t + 0.55; };
const outLine = (r, segs, t) => txt(PADX, rowY(r), segs, { mode: 'reveal', t, end: 0 });

let clk = 0.4;

// ---- SCENE 1 : shell + env ----
const s1 = { start: clk };
{
  let t = clk, r = 0;
  t = cmdLine(r++, [S('$ ', P.green)], 'poddle up api --identity work --detach', t) + 0.3;
  outLine(r++, [S('  7f3a9c2')], t); t += 0.5;
  t = cmdLine(r++, [S('$ ', P.green)], 'poddle attach api', t) + 0.3;
  t = cmdLine(r++, [S('root@7f3a9c2e1b04:/# ', P.dim)], 'env | grep ANTHROPIC', t) + 0.25;
  outLine(r++, [S('ANTHROPIC_BASE_URL=http://host.containers.internal:41207'), S(cp('', 60 - 55) + '# the broker, not Anthropic', P.dim)], t); t += 0.45;
  outLine(r++, [S('ANTHROPIC_AUTH_TOKEN='), S('poddle_kR7v3nQ8tW…', P.amber), S(cp('', 60 - 39) + '# a handle, not your key', P.dim)], t); t += 0.5;
  t = cmdLine(r++, [S('root@7f3a9c2e1b04:/# ', P.dim)], 'claude', t) + 0.35;
  s1.end = t; s1.w1 = t + 0.9;
}
clk = s1.w1 + 0.5;

// ---- SCENE 2 : Claude Code TUI ----
const s2 = { start: clk };
{
  const S2 = clk;
  const welH = 10 + 2 * LH + 10;                 // welcome box height
  const boxTop = H - 100, boxH = 46, boxInner = boxTop + 30, statusY = H - 22;
  const prompt = 'parseConfig panics on a user upload (https://pastebin.com/raw/x7Q3) — add a test + fix';
  const nP = [...prompt].length;
  const tW = S2, tBox = S2 + 0.5, tType = S2 + 1.1, tTypeEnd = tType + nP * 0.038;
  const tSubmit = tTypeEnd + 0.6; let tR = tSubmit + 0.25;
  const dot = (name, arg) => [S('⏺ ', P.dot), S(name, P.bright), S(arg, P.fg)];
  const res = (s, hint) => hint ? [S('  ⎿  ' + s + ' ', P.dim), S('(' + hint + ')', P.faint)] : [S('  ⎿  ' + s, P.dim)];

  // welcome box (full rounded border)
  boxAt(TOP, welH, tW);
  txt(PADX, TOP + 10 + asc, [S('✻ ', P.claude), S('Welcome to Claude Code', P.bright)], { mode: 'reveal', t: tW + 0.05, end: 0 });
  txt(PADX, TOP + 10 + LH + asc, [S('  /help for help   ·   cwd: /api   ·   Sonnet 4.5', P.dim)], { mode: 'reveal', t: tW + 0.12, end: 0 });

  // input box (pinned bottom) + status — present from launch
  boxAt(boxTop, boxH, tBox);
  txt(PADX, statusY, [S('  ? for shortcuts', P.faint)], { mode: 'reveal', t: tBox, end: 0 });
  txt(PADX, boxInner, [S('> ', P.dim)], { mode: 'reveal', t: tBox, end: 0 });
  const cmdX = PADX + 2 * CW;
  txt(cmdX, boxInner, [S(prompt, P.bright)], { mode: 'type', t: tType, td: nP * 0.038, n: nP, hide: tSubmit, end: 0 });
  ops.push({ kind: 'cursor', x: cmdX, y: boxInner, wins: [[tBox, tType], [tSubmit, 0]] });

  // scrollback: submitted prompt + streamed tool calls (with a live BLOCK)
  const startRow = 3;
  const lines = [
    [S('> ', P.dim), S(prompt, P.bright)],
    null,
    dot('Fetch', '(https://pastebin.com/raw/x7Q3)'),
    [S('  ⎿  ', P.dim), S('⊘ blocked by poddle', P.red), S(' — pastebin.com is not in the pod’s egress policy', P.dim)],
    [S('⏺ ', P.dot), S('Reproducing from the panic in the report instead.', P.fg)],
    dot('Update', '(parse.go)'),
    res('guard the empty-input case that panicked'),
    dot('Update', '(parse_test.go)'),
    res('add a regression test for the crash'),
    dot('Bash', '(go test ./...)'),
    res('ok · acme/api · 0.42s', 'ctrl+o to expand'),
    dot('Bash', '(git push)'),
    res('main → main · github.com/acme/api'),
    [S('⏺ ', P.dot), S('Fixed the panic + added a test — pastebin stayed off-policy.', P.fg)],
  ];
  let rr = startRow, ti = 0;
  for (const ln of lines) {
    if (ln) { outLine(rr, ln, tR + ti * 0.42); ti++; }
    rr++;
  }
  s2.end = tR + ti * 0.42; s2.w1 = s2.end + 1.3;
}
clk = s2.w1 + 0.5;

// ---- SCENE 3 : audit (held) ----
const s3 = { start: clk };
{
  let t = clk, r = 0;
  t = cmdLine(r++, [S('root@7f3a9c2e1b04:/# ', P.dim)], 'exit', t) + 0.25;
  t = cmdLine(r++, [S('$ ', P.green)], 'poddled audit', t) + 0.3;
  outLine(r++, [S(cp('TIME', 10) + cp('POD', 5) + cp('KIND', 8) + cp('DECISION', 9) + cp('UPSTREAM', 19) + 'DETAIL', P.dim)], t); t += 0.4;
  const ar = (tt, dec, dc, up, det) => [S(cp(tt, 10) + cp('api', 5) + cp('egress', 8)), S(cp(dec, 9), dc), S(cp(up, 19) + det, P.fg)];
  outLine(r++, ar('10:02:14', 'allow', P.green, 'api.anthropic.com', 'claude-code'), t); t += 0.4;
  outLine(r++, ar('10:02:15', 'redact', P.amber, 'api.anthropic.com', 'scrubbed 1 secret'), t); t += 0.4;
  outLine(r++, ar('10:02:16', 'deny', P.red, 'pastebin.com', 'not in policy'), t); t += 0.4;
  outLine(r++, ar('10:02:17', 'allow', P.green, 'github.com', 'git push'), t); t += 0.45;
  outLine(r++, [S('  audit chain: '), S('intact ✓', P.green)], t); t += 0.3;
  s3.end = t; s3.w1 = t + 4.2;
}
clk = s3.w1 + 0.4;

D = +(clk + 0.4).toFixed(2);
const scenes = [s1, s2, s3];
scenes.forEach(sc => sc.w0 = sc.start - 0.25);

// ---- emit ----
const pf = (sec) => sec / D * 100;
const fmt = (x) => x.toFixed(3);
const sceneOf = (t) => scenes.find(sc => t >= sc.w0 - 0.01 && t <= sc.w1 + 0.01) || scenes[scenes.length - 1];
function revealCss(id, t, end) {
  const pt = pf(t);
  let s = `0%,${fmt(Math.max(0, pt - 0.01))}%{opacity:0}${fmt(pt)}%`;
  s += end ? `,${fmt(pf(end))}%{opacity:1}${fmt(pf(end) + 0.01)}%,100%{opacity:0}` : `,100%{opacity:1}`;
  css += `@keyframes ${id}{${s}}.${id}{opacity:0;animation:${id} ${D}s linear infinite}`;
}
function typeCss(id, t, td, n, hide) {
  const p0 = pf(t), p1 = pf(t + td), ph = pf(hide);
  css += `@keyframes ${id}o{0%,${fmt(Math.max(0, p0 - 0.01))}%{opacity:0}${fmt(p0)}%,${fmt(ph)}%{opacity:1}${fmt(ph + 0.01)}%,100%{opacity:0}}`;
  css += `@keyframes ${id}c{0%,${fmt(p0)}%{clip-path:inset(0 100% 0 0);animation-timing-function:steps(${n})}${fmt(p1)}%,100%{clip-path:inset(0 0 0 0)}}`;
  css += `.${id}{opacity:0;clip-path:inset(0 100% 0 0);animation:${id}o ${D}s linear infinite,${id}c ${D}s linear infinite}`;
}
function winsCss(id, wins) {
  let s = '0%{opacity:0}';
  for (const [a, b] of wins) s += `${fmt(Math.max(0, pf(a) - 0.01))}%{opacity:0}${fmt(pf(a))}%{opacity:1}${fmt(pf(b))}%{opacity:1}${fmt(pf(b) + 0.01)}%{opacity:0}`;
  s += '100%{opacity:0}';
  css += `@keyframes ${id}{${s}}.${id}{opacity:0;animation:${id} ${D}s linear infinite}@keyframes blk{0%,55%{opacity:1}56%,100%{opacity:0}}`;
}
const shownAt = (a) => AT >= a.t && (!(a.hide || a.end) || AT <= (a.hide || a.end));

let body = '';
for (const op of ops) {
  if (op.kind === 'cursor') {
    const sc = sceneOf(op.wins[0][0]);
    const wins = op.wins.map(([a, b]) => [a, b || sc.w1]);
    const rect = `<rect x="${op.x.toFixed(1)}" y="${(op.y - asc + 3).toFixed(1)}" width="${CW}" height="${FS + 2}" rx="1.5" fill="${P.bright}" opacity=".9"/>`;
    if (AT !== null) body += `<g opacity="${wins.some(([a, b]) => AT >= a && AT <= b) ? 1 : 0}">${rect}</g>`;
    else { const id = 'c' + (uid++); winsCss(id, wins); body += `<g class="${id}"><g style="animation:blk 1.06s steps(1,end) infinite">${rect}</g></g>`; }
    continue;
  }
  const sc = sceneOf(op.anim.t);
  const end = !op.anim.end && !op.anim.hide ? sc.w1 : op.anim.end;
  let cls;
  if (AT !== null) cls = `opacity="${shownAt({ t: op.anim.t, hide: op.anim.hide, end: end || sc.w1 }) ? 1 : 0}"`;
  else {
    const id = 'a' + (uid++);
    if (op.anim.mode === 'type') typeCss(id, op.anim.t, op.anim.td, op.anim.n, op.anim.hide);
    else revealCss(id, op.anim.t, end);
    cls = `class="${id}"`;
  }
  if (op.kind === 'box') body += `<g ${cls}><rect x="${op.x}" y="${op.y.toFixed(1)}" width="${op.w}" height="${op.h}" rx="9" fill="none" stroke="${P.boxln}"/></g>`;
  else body += `<text ${cls} x="${op.x}" y="${op.y.toFixed(1)}">${tsp(op.segs)}</text>`;
}

const frame =
  `<defs><clipPath id="rc"><rect width="${W}" height="${H}" rx="14"/></clipPath></defs>`
  + `<g clip-path="url(#rc)"><rect width="${W}" height="${H}" fill="${P.ink}"/>`
  + `<rect x="24" y="${BAR / 2 - 8}" width="16" height="16" rx="5" fill="${P.mark}"/>`
  + `<circle cx="32" cy="${BAR / 2}" r="3" fill="${P.ink}" fill-opacity=".85"/>`
  + `<text class="wm" x="50" y="${BAR / 2}" font-size="18" fill="${P.bone}" dominant-baseline="middle">poddle</text>`
  + `<text x="${W - 24}" y="${BAR / 2}" font-size="12.5" fill="${P.bone}" fill-opacity=".4" text-anchor="end" dominant-baseline="middle">sandbox · api</text>`
  + `<line x1="16" y1="${BAR}" x2="${W - 16}" y2="${BAR}" stroke="${P.bone}" stroke-opacity=".08"/>`
  + body + `</g>`
  + `<rect x="0.5" y="0.5" width="${W - 1}" height="${H - 1}" rx="14" fill="none" stroke="${P.bone}" stroke-opacity=".07"/>`;

fs.writeFileSync(OUT, `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}"><style>${css}</style>${frame}</svg>`);
console.error(`3 scenes, ${D}s loop, ${W}x${H}${AT !== null ? ` @${AT}s` : ''} -> ${OUT}  (s1<${s1.w1.toFixed(1)} s2<${s2.w1.toFixed(1)} s3<${s3.w1.toFixed(1)})`);
