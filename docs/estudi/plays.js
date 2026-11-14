// The map and the plays, shared by the phone app and the PC app (which loads this file from here).
// The map is Riot's official Summoner's Rift minimap (Data Dragon, map11.png); coordinates go 0-100 with your base
// bottom-left, from the game's own units (0-14870). The towers and pits are where they are in the game; if the
// image can't load (no connection), a sketch of the lanes and the river stays underneath.
(function(){
"use strict";
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"}[c]));
const SY = y => 100 - y;
const C = {card: "var(--card, var(--panel, #fff))", line: "var(--line, #ccc)", fg: "var(--fg, #111)", muted: "var(--muted, #666)",
  blue: "var(--accent, #137a6b)", red: "var(--bad, var(--loss, #b4442e))", gold: "var(--gold, #c9a34e)", chip: "var(--chip, var(--cool, #ddd))"};
// game units / 148.7 (blue = your team)
const TOWERS = {b: [[6.6, 70.2], [10.2, 45], [7.9, 28.8], [39.3, 43], [33.9, 32.4], [24.6, 24.9], [70.6, 6.9], [46.5, 10], [28.8, 8.4]],
  r: [[29, 93.3], [53.4, 90.2], [70.5, 91.8], [60.2, 57.2], [65.7, 68], [74.9, 75.4], [93.2, 30.3], [89.6, 55.3], [91.6, 71.1]]};
const PITS = {drac: [66.3, 29.7], baro: [33.7, 70.4], larves: [33.7, 70.4], herald: [33.7, 70.4]};
let ddv = "14.24.1";
try { ddv = localStorage.getItem("rm.ddv2") || ddv; } catch (e) {}
fetch("https://ddragon.leagueoflegends.com/api/versions.json").then(r => r.json()).then(v => { if (v?.[0]) { ddv = v[0]; try { localStorage.setItem("rm.ddv2", ddv); } catch (e) {} } }).catch(() => {});
const DDID = {"LeBlanc": "Leblanc", "Wukong": "MonkeyKing", "Kai'Sa": "Kaisa", "Cho'Gath": "Chogath", "Vel'Koz": "Velkoz", "Kha'Zix": "Khazix", "Bel'Veth": "Belveth", "Renata Glasc": "Renata", "Nunu & Willump": "Nunu"};
const ddId = n => DDID[n] || String(n || "Ahri").replace(/[^A-Za-z]/g, "").replace(/^./, c => c.toUpperCase());
const champImg = c => `https://ddragon.leagueoflegends.com/cdn/${ddv}/img/champion/${ddId(c)}.png`;
const mapImg = () => `https://ddragon.leagueoflegends.com/cdn/${ddv}/img/map/map11.png`;
let uid = 0;

function base(id){
  return `<rect x="0" y="0" width="100" height="100" rx="3" fill="${C.card}"/>
    <path d="M14 14L86 86" stroke="${C.blue}" stroke-opacity=".14" stroke-width="11" stroke-linecap="round"/>
    <path d="M12 88L88 12" stroke="${C.line}" stroke-width="4.5" stroke-linecap="round"/>
    <path d="M8 96V8H96" fill="none" stroke="${C.line}" stroke-width="5" stroke-linejoin="round"/>
    <path d="M4 92H92V4" fill="none" stroke="${C.line}" stroke-width="5" stroke-linejoin="round"/>
    <image href="${mapImg()}" x="0" y="0" width="100" height="100" preserveAspectRatio="none" onerror="this.remove()"/>
    ${TOWERS.b.map(([x, y]) => `<rect x="${x - 1.3}" y="${SY(y) - 1.3}" width="2.6" height="2.6" rx=".5" fill="#2f8fd8" stroke="#fff" stroke-width=".35"/>`).join("")}
    ${TOWERS.r.map(([x, y]) => `<rect x="${x - 1.3}" y="${SY(y) - 1.3}" width="2.6" height="2.6" rx=".5" fill="#d84a3a" stroke="#fff" stroke-width=".35"/>`).join("")}
    <defs>${["g", "b", "r"].map(k => `<marker id="ah${k}${id}" viewBox="0 0 10 10" refX="7" refY="5" markerWidth="4" markerHeight="4" orient="auto"><path d="M0 0L10 5L0 10z" fill="${k === "b" ? "#3fb4ff" : k === "r" ? "#ff5a48" : "#f2c14e"}"/></marker>`).join("")}
      <clipPath id="cc${id}"><circle cx="0" cy="0" r="2.75"/></clipPath></defs>`;
}
const lab = (x, y, t, dy = -3.2) => t ? `<text x="${x}" y="${SY(y) + dy}" text-anchor="middle" font-size="3.2" font-weight="700" fill="#fff" paint-order="stroke" stroke="#000" stroke-opacity=".75" stroke-width=".9">${esc(t)}</text>` : "";
const ward = (x, y) => `<path d="M${x} ${SY(y) - 2.2}l1.6 2.2-1.6 2.2-1.6-2.2z" fill="#f2c14e" stroke="#000" stroke-width=".3"/>`;
const arrow = (id, x1, y1, x2, y2, c = "g") => `<line x1="${x1}" y1="${SY(y1)}" x2="${x2}" y2="${SY(y2)}" stroke="${c === "b" ? "#3fb4ff" : c === "r" ? "#ff5a48" : "#f2c14e"}" stroke-width="1.1" ${c === "r" ? 'stroke-dasharray="2 1.2"' : ""} marker-end="url(#ah${c}${id})"/>`;

// a lesson's map: {k: arrow|zone|ward|dot, x, y, x2, y2, r, t}
function mapSVG(marks){
  const id = ++uid;
  let s = `<svg viewBox="-1 -1 102 102" role="img" aria-label="Mapa de la Grieta">${base(id)}`;
  for (const m of marks || []) {
    if (m.k === "arrow") s += arrow(id, m.x, m.y, m.x2, m.y2) + lab((m.x + m.x2) / 2, (m.y + m.y2) / 2, m.t);
    else if (m.k === "zone") s += `<circle cx="${m.x}" cy="${SY(m.y)}" r="${m.r}" fill="#f2c14e" fill-opacity=".25" stroke="#f2c14e" stroke-width=".5"/>` + lab(m.x, m.y + m.r, m.t, -1);
    else if (m.k === "ward") s += ward(m.x, m.y);
    else s += `<circle cx="${m.x}" cy="${SY(m.y)}" r="1.6" fill="#fff" stroke="#000" stroke-width=".3"/>` + lab(m.x, m.y, m.t);
  }
  return s + `</svg>`;
}

// the state after a step: champions keep their place until a step moves them
function state(p, upto, me){
  const st = {u: {}, w: {}, o: [], a: [], wd: []};
  p.steps.slice(0, upto + 1).forEach((s, i) => {
    for (const [id, v] of Object.entries(s.u || {})) {
      if (v === "dead") st.u[id] = {...st.u[id], dead: true};
      else if (v.length === 4) st.u[id] = {c: v[0] === "@me" ? (me || "Ahri") : v[0], t: v[1], x: v[2], y: v[3], me: id === "me"};
      else st.u[id] = {...st.u[id], x: v[0], y: v[1]};
    }
    for (const [id, v] of Object.entries(s.w || {})) st.w[id] = {t: v[0], x: v[1], y: v[2], n: v[3]};
    if (s.o) st.o = s.o;
    if (s.wd) st.wd = st.wd.concat(s.wd);
    st.a = i === upto ? (s.a || []) : [];
  });
  return st;
}
function playSVG(p){
  const id = ++uid, ids = [];
  for (const s of p.steps) for (const [k, v] of Object.entries(s.u || {})) if (Array.isArray(v) && v.length === 4 && !ids.includes(k)) ids.push(k);
  return `<svg viewBox="-1 -1 102 102" role="img" aria-label="${esc(p.name)}" class="rmplay" data-id="${id}">${base(id)}<g class="pobj"></g><g class="pwd"></g><g class="pw"></g><g class="parr"></g>
    ${ids.map(k => `<g class="unit" data-u="${k}" style="transition:transform .9s ease,opacity .5s"></g>`).join("")}</svg>`;
}
function draw(svg, p, i, me){
  if (!svg) return;
  const id = svg.dataset.id, st = state(p, i, me);
  svg.querySelector(".pobj").innerHTML = st.o.map(o => { const [x, y] = PITS[o.k] || [o.x, o.y];
    return `<circle cx="${x}" cy="${SY(y)}" r="4.2" fill="#ff5a48" fill-opacity="${o.dead ? .12 : .35}" stroke="#ff5a48" stroke-width=".6"/>${lab(x, y, ({drac: "Drac", baro: "Baró", larves: "Larves", herald: "Herald"}[o.k] || o.k) + (o.t ? " " + o.t : ""), 7.5)}`; }).join("");
  svg.querySelector(".pwd").innerHTML = st.wd.map(([x, y]) => ward(x, y)).join("");
  svg.querySelector(".pw").innerHTML = Object.values(st.w).filter(w => w.n > 0).map(w => { const c = w.t === "b" ? "#3fb4ff" : "#ff5a48", k = Math.min(w.n, 15);
    return `<g transform="translate(${w.x} ${SY(w.y)})">${Array.from({length: k}, (_, j) => `<rect x="${(j % 5) * 1.5 - 3.6}" y="${Math.floor(j / 5) * 1.5 - 1.4}" width="1.2" height="1.2" fill="${c}" stroke="#000" stroke-width=".2"/>`).join("")}</g>`; }).join("");
  svg.querySelector(".parr").innerHTML = st.a.map(a => arrow(id, ...a)).join("");
  for (const [k, u] of Object.entries(st.u)) {
    const g = svg.querySelector(`.unit[data-u="${k}"]`); if (!g) continue;
    if (!g.dataset.c) { g.dataset.c = u.c; const ring = u.me ? "#f2c14e" : u.t === "b" ? "#3fb4ff" : "#ff5a48";
      g.innerHTML = `<circle r="3.3" fill="${ring}"/><circle r="2.75" fill="#20292b"/><text y="1" text-anchor="middle" font-size="2.6" fill="#fff">${esc(u.c.slice(0, 2))}</text>
        <image href="${champImg(u.c)}" x="-2.75" y="-2.75" width="5.5" height="5.5" clip-path="url(#cc${id})" onerror="this.remove()"/>${u.me ? `<text y="-4.3" text-anchor="middle" font-size="2.8" font-weight="700" fill="#f2c14e" paint-order="stroke" stroke="#000" stroke-width=".8">Tu</text>` : ""}
        <path class="x" d="M-2.6 -2.6L2.6 2.6M2.6 -2.6L-2.6 2.6" stroke="#ff5a48" stroke-width="1" opacity="0"/>`; }
    const first = !g.dataset.placed; g.dataset.placed = 1;
    if (first) g.style.transition = "none";
    g.style.transform = `translate(${u.x}px, ${SY(u.y)}px)`;
    if (first) { g.getBoundingClientRect(); g.style.transition = "transform .9s ease,opacity .5s"; }
    g.style.opacity = u.dead ? .35 : 1; g.querySelector(".x").setAttribute("opacity", u.dead ? 1 : 0);
  }
}
window.RMPlays = {mapSVG, playSVG, draw, champImg, ddId};
})();
