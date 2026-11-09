package main

// overlayPage: the small window above the game. It shows the timers you start by voice (the same state the
// phone gets); with clicks on, tapping a spell starts or cancels it, as the Ctrl+Shift/Ctrl+Alt keys do.
const overlayPage = `<!doctype html><html lang="ca"><head><meta charset="utf-8"><title>RM·Overlay</title>
<style>
:root{--bg:#0b1210;--fg:#e8efec;--muted:#8a9d96;--line:#22302b;--run:#ff9a4a;--ok:#2fc49c;color-scheme:dark}
*{box-sizing:border-box}html,body{margin:0;height:100%;overflow:hidden;background:var(--bg);color:var(--fg);font:13px/1.25 "Segoe UI",system-ui,sans-serif;user-select:none}
body{padding:6px 7px;border:1px solid var(--line)}
.top{display:flex;justify-content:space-between;color:var(--muted);font-size:11px;margin-bottom:4px;font-variant-numeric:tabular-nums}
.obj{display:flex;gap:4px;flex-wrap:wrap;margin-bottom:4px}.obj span{background:#16211e;border-radius:6px;padding:1px 6px;font-size:11px;font-variant-numeric:tabular-nums}
.r{display:grid;grid-template-columns:1fr 62px 62px;gap:4px;align-items:center;padding:3px 0;border-top:1px solid var(--line)}
.n{font-weight:650;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.n small{color:var(--muted);font-weight:500;margin-left:4px;font-size:10px}
.s{border:0;border-radius:6px;padding:4px 2px;background:#16211e;color:var(--muted);font:600 12px system-ui;font-variant-numeric:tabular-nums;cursor:pointer;text-align:center}
.s b{display:block;font-size:9px;font-weight:600;opacity:.75;text-transform:uppercase}
.s.run{background:color-mix(in srgb,var(--run) 25%,#16211e);color:var(--run)}.s.ok{color:var(--ok)}
.s.new{background:var(--run);color:#111}
.empty{color:var(--muted);text-align:center;margin-top:40%;font-size:12px}
</style></head><body>
<div class="top"><span>RiftMate</span><span id="t"></span></div><div class="obj" id="obj"></div><div id="rows"><p class="empty">Esperant la partida…</p></div>
<script>
let st=null,got=0,was={},since={};
const fmt=s=>{s=Math.max(0,Math.round(s));return Math.floor(s/60)+":"+String(s%60).padStart(2,"0")};
async function poll(){try{const r=await fetch("/api/mirror",{cache:"no-store"});st=await r.json();got=Date.now()}catch(e){}}
async function tap(row,kind){try{await fetch("/api/hotkeys?row="+row+"&kind="+kind,{method:"POST"})}catch(e){}setTimeout(poll,400)}
function draw(){const el=document.getElementById("rows");
 if(!st||!st.rows||!st.rows.length){el.innerHTML='<p class="empty">Esperant la partida…</p>';document.getElementById("obj").innerHTML="";document.getElementById("t").textContent="";return}
 const dt=(Date.now()-got)/1000;document.getElementById("t").textContent=st.time!=null?fmt(st.time+dt):"";
 document.getElementById("obj").innerHTML=(st.obj||[]).filter(o=>o.left-dt>-60).map(o=>"<span>"+o.name+" "+(o.left-dt>0?fmt(o.left-dt):"ja")+"</span>").join("");
 const now={};
 el.innerHTML=st.rows.map(r=>'<div class="r"><div class="n">'+r.name+'<small>'+(r.lane||"")+'</small></div>'+r.spells.map(s=>{const l=s.left-dt,k=r.row+s.kind;now[k]=l>0;
  if(l>0&&!was[k])since[k]=Date.now();const fresh=l>0&&Date.now()-(since[k]||0)<2000;return '<button class="s '+(l>0?"run":"ok")+(fresh?" new":"")+'" onclick="tap('+r.row+',\''+s.kind+'\')">'+(l>0?fmt(l):"✓")+"<b>"+s.spell+"</b></button>"}).join("")+"</div>").join("");
 was=now}
poll();setInterval(poll,1000);setInterval(draw,250);
</script></body></html>`
