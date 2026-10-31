# RiftMate

Coach de veu, temporitzadors d'encanteris i informes de partida per a League of Legends (Windows).

## Instal·lar

1. Descarrega `RiftMate.exe` de l'última versió a [Releases](https://github.com/DidiLlam/RiftMate/releases/latest).
2. Obre'l i accepta la instal·lació: es crea un accés directe a l'escriptori.
3. A partir de la 2.0.0 l'app avisa sola quan hi ha una versió nova i s'actualitza amb un clic.

## Compilar

```
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o RiftMate.exe .
```

Les dades (informes, ajustos, claus) es guarden només a l'ordinador, a `%LOCALAPPDATA%\RiftMate`.

---

RiftMate no està avalat per Riot Games i no reflecteix les opinions de Riot Games ni de ningú implicat oficialment en la producció o gestió de League of Legends. League of Legends i Riot Games són marques comercials o marques registrades de Riot Games, Inc.
