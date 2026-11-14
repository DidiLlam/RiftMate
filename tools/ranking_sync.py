"""Keeps the Best plays ranking forever. The apps send their plays to an ntfy.sh channel, which forgets them after
12 hours (the clips after 3); this runs every few minutes on GitHub, reads the channel, and keeps everything in the
"ranking" release of the repo: ranking.json (the list) and one .mp4 per clip. The apps read them from there."""
import json, os, re, subprocess, sys, time, urllib.request

TOPIC = "riftmate-millors-jugades-v1-7q4k"
BASE = "https://ntfy.sh"
REPO = os.environ.get("GITHUB_REPOSITORY", "DidiLlam/RiftMate")
TAG = "ranking"
MIN, MAX_PLAYS, MAX_CLIP = 90, 500, 16 << 20
ID = re.compile(r"^[0-9a-f]{16}$")

def gh(*a, check=True):
    return subprocess.run(["gh", *a], check=check, capture_output=True, text=True)

def get(url, limit=MAX_CLIP + 1):
    req = urllib.request.Request(url, headers={"User-Agent": "riftmate-ranking"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read(limit)

def main():
    if gh("release", "view", TAG, check=False).returncode != 0:
        gh("release", "create", TAG, "--prerelease", "--title", "Rànquing de Best plays",
           "--notes", "Dades del rànquing compartit de l'app (no és una versió de l'app).")
    data = {"plays": {}, "since": "all"}
    if gh("release", "download", TAG, "-p", "ranking.json", "-D", "/tmp/rk", "--clobber", check=False).returncode == 0:
        data = json.load(open("/tmp/rk/ranking.json"))
    plays, changed = data["plays"], False
    try:
        body = get(f"{BASE}/{TOPIC}/json?poll=1&since={data.get('since') or 'all'}", 8 << 20).decode()
    except Exception as e:
        print("ntfy:", e); return
    for line in body.splitlines():
        try:
            m = json.loads(line)
        except ValueError:
            continue
        if m.get("event") != "message":
            continue
        data["since"] = m["id"]
        try:
            e = json.loads(m.get("message") or "")
        except ValueError:
            continue
        if not isinstance(e, dict) or not ID.match(str(e.get("id", ""))) or not isinstance(e.get("s"), int) or not MIN <= e["s"] <= 100 or not str(e.get("n", "")).strip():
            continue
        clean = {k: e[k] for k in ("id", "n", "c", "s", "t", "w", "k", "at") if k in e}
        clean["n"], clean["t"], clean["w"] = str(clean["n"])[:24], str(clean.get("t", ""))[:60], str(clean.get("w", ""))[:300]
        old = plays.get(e["id"])
        if old is None or old.get("s") != clean["s"]:
            clean["vurl"] = (old or {}).get("vurl", "")
            clean["first"] = (old or {}).get("first", int(time.time()))
            plays[e["id"]] = clean
            changed = True
        att = m.get("attachment") or {}
        if att.get("url", "").startswith(BASE + "/file/") and not plays[e["id"]].get("vurl") and att.get("size", 0) < MAX_CLIP:
            try:
                b = get(att["url"])
                if len(b) < MAX_CLIP and b[4:8] == b"ftyp":
                    path = f"/tmp/{e['id']}.mp4"
                    open(path, "wb").write(b)
                    gh("release", "upload", TAG, path, "--clobber")
                    plays[e["id"]]["vurl"] = f"https://github.com/{REPO}/releases/download/{TAG}/{e['id']}.mp4"
                    changed = True
            except Exception as x:
                print("clip", e["id"], x)
    if len(plays) > MAX_PLAYS:
        keep = sorted(plays.values(), key=lambda p: (p["s"], p.get("at", 0)), reverse=True)[:MAX_PLAYS]
        gone = set(plays) - {p["id"] for p in keep}
        for i in gone:
            if plays[i].get("vurl"):
                gh("release", "delete-asset", TAG, f"{i}.mp4", "-y", check=False)
        data["plays"] = {p["id"]: p for p in keep}
        changed = True
    data["updated"] = int(time.time())
    os.makedirs("/tmp/out", exist_ok=True)
    json.dump(data, open("/tmp/out/ranking.json", "w"), ensure_ascii=False)
    gh("release", "upload", TAG, "/tmp/out/ranking.json", "--clobber")
    print(f"{len(data['plays'])} jugades; canvis: {changed}")

if __name__ == "__main__":
    main()
