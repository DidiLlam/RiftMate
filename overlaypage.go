package main

// overlayPage: the small window above the game. It shows the timers you start by voice (the same state the
// phone gets); with clicks on, tapping a spell starts or cancels it, as the Ctrl+Shift/Ctrl+Alt keys do.
const overlayPage = `<!doctype html><html lang="ca"><head><meta charset="utf-8"><title>RM·Overlay</title>
<style>
:root{--key:#010101;--plate:#0f1815;--fg:#eef4f1;--muted:#9fb2ab;--run:#ff9a4a;--ok:#33c99f;color-scheme:dark}
*{box-sizing:border-box}html,body{margin:0;height:100%;overflow:hidden;background:var(--key);color:var(--fg);font:13px/1.25 "Segoe UI",system-ui,sans-serif;user-select:none}
body{padding:4px}
.top{display:inline-flex;gap:6px;background:var(--plate);border-radius:8px;padding:2px 8px;margin-bottom:4px;font-size:11px;color:var(--muted);font-variant-numeric:tabular-nums}
.top b{color:var(--fg);font-weight:600}
.r{display:grid;grid-template-columns:1fr 58px 58px;gap:3px;align-items:center;background:var(--plate);border-radius:8px;padding:3px 3px 3px 8px;margin-bottom:3px}
.n{font-weight:650;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.s{border:0;border-radius:6px;padding:3px 2px;background:#1a2622;color:var(--muted);font:600 12px system-ui;font-variant-numeric:tabular-nums;cursor:pointer;text-align:center}
.s b{display:block;font-size:9px;font-weight:600;opacity:.75;text-transform:uppercase}
.s.run{background:#4a2e17;color:var(--run)}.s.ok{color:var(--ok)}.s.new{background:var(--run);color:#111}
.empty{display:inline-block;background:var(--plate);border-radius:8px;padding:3px 8px;color:var(--muted);font-size:11px}
</style></head><body>
<div id="rows"><span class="empty">RiftMate · esperant la partida…</span></div>
<script>
let st=null,got=0,was={},since={};
const fmt=s=>{s=Math.max(0,Math.round(s));return Math.floor(s/60)+":"+String(s%60).padStart(2,"0")};
async function poll(){try{const r=await fetch("/api/mirror",{cache:"no-store"});st=await r.json();got=Date.now()}catch(e){}}
async function tap(row,kind){try{await fetch("/api/hotkeys?row="+row+"&kind="+kind,{method:"POST"})}catch(e){}setTimeout(poll,400)}
// redrawn once a second, and only if something on it changed: the less it repaints, the less it costs the game
let last="";
function draw(){const el=document.getElementById("rows");let h;
 if(!st||!st.rows||!st.rows.length)h='<span class="empty">RiftMate · esperant la partida…</span>';
 else{const dt=(Date.now()-got)/1000,now={};
  const obj=(st.obj||[]).filter(o=>o.left-dt>-60).map(o=>" · "+o.name+" <b>"+(o.left-dt>0?fmt(o.left-dt):"ja")+"</b>").join("");
  h='<div class="top"><b>'+(st.time!=null?fmt(st.time+dt):"")+"</b>"+obj+"</div>"+st.rows.map(r=>'<div class="r"><div class="n">'+r.name+"</div>"+r.spells.map(s=>{const l=s.left-dt,k=r.row+s.kind;now[k]=l>0;
   if(l>0&&!was[k])since[k]=Date.now();const fresh=l>0&&Date.now()-(since[k]||0)<2000;
   return '<button class="s '+(l>0?"run":"ok")+(fresh?" new":"")+'" onclick="tap('+r.row+',\''+s.kind+'\')">'+(l>0?fmt(l):"✓")+"<b>"+s.spell+"</b></button>"}).join("")+"</div>").join("");
  was=now}
 if(h!==last){el.innerHTML=h;last=h}}
poll();setInterval(poll,1000);setInterval(draw,1000);
</script></body></html>`
