package main

// mobilePage: the phone screen (served on the home network with the secret). Big, dark, one hand.
const mobilePage = `<!doctype html><html lang="ca"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#0c1311"><title>RiftMate</title>
<style>
:root{--bg:#0c1311;--panel:#141e1b;--fg:#e6eeeb;--muted:#8fa39c;--line:#26332f;--blue:#4b93ff;--orange:#ff8f3a;--ready:#2fc49c;color-scheme:dark}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.35 "Segoe UI",system-ui,sans-serif;padding:max(12px,env(safe-area-inset-top)) max(12px,env(safe-area-inset-right)) 24px max(12px,env(safe-area-inset-left))}
h1{font-size:1rem;margin:0 0 10px;display:flex;justify-content:space-between;color:var(--muted);font-weight:600}
.row{background:var(--panel);border:1px solid var(--line);border-radius:14px;padding:10px 12px;margin-bottom:8px;display:grid;grid-template-columns:1fr auto auto;gap:8px;align-items:center}
.n{font-weight:700;font-size:1.05rem}.n small{display:block;color:var(--muted);font-weight:500;font-size:.75rem}
button{border:0;border-radius:12px;min-width:92px;padding:12px 8px;font:700 1rem system-ui;background:#1d2a26;color:var(--ready);font-variant-numeric:tabular-nums}
button.run{background:color-mix(in srgb,var(--orange) 22%,#1d2a26);color:var(--orange)}button span{display:block;font-size:.7rem;color:var(--muted);font-weight:600}
.obj{display:flex;gap:8px;flex-wrap:wrap;margin:6px 0 12px}.obj div{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:6px 10px;font-variant-numeric:tabular-nums}
.empty{color:var(--muted);text-align:center;margin-top:30vh}
</style></head><body>
<h1><span>RiftMate</span><span id="t"></span></h1><div class="obj" id="obj"></div><div id="rows"><p class="empty">Esperant la partida…</p></div>
<script>
const T=new URLSearchParams(location.search).get("t");let st=null,got=0;
const fmt=s=>{s=Math.max(0,Math.round(s));return Math.floor(s/60)+":"+String(s%60).padStart(2,"0")};
async function poll(){try{const r=await fetch("/m/state?t="+T,{cache:"no-store"});st=await r.json();got=Date.now()}catch(e){}}
async function tap(row,kind){try{await fetch("/m/tap?t="+T+"&row="+row+"&kind="+kind,{method:"POST"});navigator.vibrate&&navigator.vibrate(40)}catch(e){}setTimeout(poll,500)}
function draw(){const el=document.getElementById("rows");if(!st||!st.rows||!st.rows.length){el.innerHTML='<p class="empty">Esperant la partida…</p>';document.getElementById("obj").innerHTML="";document.getElementById("t").textContent="";return}
 const dt=(Date.now()-got)/1000;document.getElementById("t").textContent=st.time!=null?fmt(st.time+dt):"";
 document.getElementById("obj").innerHTML=(st.obj||[]).map(o=>"<div>"+o.name+" "+(o.left-dt>0?fmt(o.left-dt):"ja")+"</div>").join("");
 el.innerHTML=st.rows.map(r=>'<div class="row"><div class="n">'+r.name+'<small>'+(r.lane||"")+'</small></div>'+r.spells.map(s=>{const l=s.left-dt;return '<button class="'+(l>0?"run":"")+'" onclick="tap('+r.row+',\''+s.kind+'\')">'+(l>0?fmt(l):"llest")+"<span>"+s.spell+"</span></button>"}).join("")+"</div>").join("")}
poll();setInterval(poll,1000);setInterval(draw,250);
</script></body></html>`
