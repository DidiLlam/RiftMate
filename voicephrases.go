package main

import (
	"sort"
	"strings"
)

// How Spanish/Catalan speakers say champion names, as the recognizers tend to write them.
// Used twice: the Windows recognizer listens for these forms, and the page matches them back to the champion.
var voiceVariants = map[string][]string{
	"Ahri": {"Ari"}, "Akshan": {"Akshan"}, "Ambessa": {"Ambesa"}, "Amumu": {"Amumu", "A mumu"}, "Anivia": {"Anibia"}, "Annie": {"Ani"},
	"Aphelios": {"Afelios"}, "Ashe": {"Ash"}, "Aurelion Sol": {"Aurelion", "Asol"}, "Azir": {"Asir"}, "Bel'Veth": {"Belvez", "Belvet"},
	"Blitzcrank": {"Blitz"}, "Briar": {"Braiar"}, "Caitlyn": {"Keitlin", "Caitlin"}, "Camille": {"Camil"}, "Cassiopeia": {"Casiopea"},
	"Cho'Gath": {"Chogat", "Chogath"}, "Dr. Mundo": {"Mundo"}, "Draven": {"Dreiven"}, "Ekko": {"Eko"}, "Elise": {"Elis"}, "Evelynn": {"Evelin"},
	"Fiddlesticks": {"Fiddle", "Fidel"}, "Fizz": {"Fis"}, "Gnar": {"Nar"}, "Graves": {"Greivs"}, "Gwen": {"Guen"}, "Hecarim": {"Jecarim"},
	"Heimerdinger": {"Heimer", "Jaimer"}, "Hwei": {"Juei", "Guei"}, "Illaoi": {"Ilaoi"}, "Ivern": {"Aivern"}, "Janna": {"Yana"}, "Jarvan IV": {"Jarvan", "Yarvan"},
	"Jax": {"Yax"}, "Jayce": {"Yeis"}, "Jhin": {"Yin"}, "Jinx": {"Yinx"}, "K'Sante": {"Kasante", "Ksante"}, "Kai'Sa": {"Kaisa", "Caisa"},
	"Karthus": {"Kartus"}, "Kassadin": {"Kasadin"}, "Katarina": {"Catarina"}, "Kayle": {"Keil", "Kail"}, "Kayn": {"Kein", "Cain"},
	"Kennen": {"Kenen"}, "Kha'Zix": {"Kasix", "Kazix"}, "Kog'Maw": {"Kogmau", "Cogmau"}, "LeBlanc": {"Leblan", "Le blanc"}, "Lee Sin": {"Li Sin"},
	"Lillia": {"Lilia"}, "Lissandra": {"Lisandra"}, "Lucian": {"Lusian"}, "Malphite": {"Malfait", "Malfite"}, "Malzahar": {"Malzajar", "Malsajar"},
	"Master Yi": {"Yi"}, "Miss Fortune": {"Miss Fortune"}, "Mordekaiser": {"Morde"}, "Naafiri": {"Nafiri"}, "Neeko": {"Niko"}, "Nidalee": {"Nidali"},
	"Nilah": {"Nila"}, "Nunu & Willump": {"Nunu"}, "Orianna": {"Oriana"}, "Ornn": {"Orn"}, "Pantheon": {"Panteon"}, "Poppy": {"Popi"},
	"Pyke": {"Paik"}, "Qiyana": {"Kiyana"}, "Quinn": {"Kuin"}, "Rammus": {"Ramus"}, "Rek'Sai": {"Reksai"}, "Rell": {"Rel"}, "Rumble": {"Rambel"},
	"Ryze": {"Rais"}, "Senna": {"Sena"}, "Seraphine": {"Serafin"}, "Sejuani": {"Seyuani"}, "Sett": {"Set"}, "Shaco": {"Chaco"}, "Shyvana": {"Shivana", "Chivana"},
	"Sivir": {"Sibir"}, "Swain": {"Suein"}, "Sylas": {"Sailas", "Silas"}, "Syndra": {"Sindra"}, "Tahm Kench": {"Tam Kench", "Tam"}, "Taliyah": {"Talia"},
	"Teemo": {"Timo"}, "Thresh": {"Tresh", "Trech"}, "Tryndamere": {"Trindamir", "Trinda"}, "Twisted Fate": {"Tuisted"}, "Twitch": {"Tuich"},
	"Udyr": {"Udir"}, "Varus": {"Barus"}, "Vayne": {"Vein", "Bein"}, "Veigar": {"Beigar"}, "Vel'Koz": {"Velkoz", "Belkoz"}, "Vex": {"Bex"},
	"Viego": {"Biego"}, "Viktor": {"Biktor"}, "Vladimir": {"Vlad", "Bladimir"}, "Volibear": {"Bolibear"}, "Warwick": {"Guaruik", "Warwik"},
	"Wukong": {"Bukong", "Gukong"}, "Xayah": {"Chaya", "Shaya"}, "Xerath": {"Cherath", "Seraz"}, "Xin Zhao": {"Xin", "Chin Chao", "Sin Chao"},
	"Yorick": {"Iorik", "Yorik"}, "Yuumi": {"Yumi"}, "Zac": {"Sac"}, "Zed": {"Sed"}, "Zeri": {"Seri"}, "Ziggs": {"Sigs"}, "Zilean": {"Silean"},
	"Zoe": {"Soe"}, "Zyra": {"Sira", "Zaira"},
}

// Words the recognizer listens for. The page maps them back with its own vocabulary.
// English names and the Spanish client's ones (Destello, Prender, Curar, Teleportar, Extenuación, Barrera, Fantasma, Limpiar)
var voiceSpells = []string{"flash", "flas", "destello", "ignite", "ignait", "prender", "heal", "jil", "curar", "teleport", "teletransporte", "teleportar",
	"exhaust", "exaust", "extenuación", "barrera", "barrier", "ghost", "gost", "fantasma", "cleanse", "clins", "purificar", "limpiar",
	"zhonya", "zonia", "reloj", "ulti", "ultimate", "definitiva"}
var voiceLanes = []string{"top", "jungla", "jungle", "mid", "adc", "bot", "suport", "support", "soporte"}

// Wake words for the AI coach: after one of these, the next sentence is a free question.
// "coach" isn't a Spanish word, so its Spanish spellings ("couch", "cauch") and "entrenador" are accepted too.
var voiceWake = []string{"coach", "oye coach", "escolta coach", "hey coach", "pregunta", "pregunta coach", "couch", "cauch", "kouch",
	"entrenador", "oye entrenador", "escolta entrenador"}

// spokenName: the champion name without punctuation, as a recognizer phrase
func spokenName(c string) string {
	repl := map[string]string{"Dr. Mundo": "Mundo", "Nunu & Willump": "Nunu", "Jarvan IV": "Jarvan"}
	if r, ok := repl[c]; ok {
		c = r
	}
	c = strings.NewReplacer("'", "", ".", "", "&", "").Replace(c)
	return strings.Join(strings.Fields(c), " ")
}

// spokenForms: the name plus the ways people say it
func spokenForms(c string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range append([]string{spokenName(c)}, voiceVariants[c]...) {
		if k := strings.ToLower(f); f != "" && !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}

// voicePhrases: every phrase the Windows recognizer accepts. With the champions of the current game it listens
// only for them (and their spoken forms), which is far more accurate than all 170; "top Darius" style phrases
// keep every champion so the rivals can still be named by voice if detection failed.
func voicePhrases(game []string) []string {
	targets := []string{}
	known := map[string]bool{}
	for _, c := range allChampions() {
		known[c] = true
	}
	for _, c := range game {
		if known[c] {
			targets = append(targets, spokenForms(c)...)
		}
	}
	if len(targets) == 0 {
		for _, c := range allChampions() {
			targets = append(targets, spokenName(c))
		}
	}
	set := map[string]bool{}
	add := func(s string) { set[strings.Join(strings.Fields(s), " ")] = true }
	for _, sp := range voiceSpells {
		for _, t := range targets {
			add(sp + " " + t)
			add(sp + " de " + t)
			add("cancela " + sp + " " + t)
			add("cancela " + sp + " de " + t)
			add(t + " " + sp)
		}
		for _, l := range voiceLanes {
			add(sp + " " + l)
			add(sp + " del " + l)
			add("cancela " + sp + " " + l)
		}
	}
	for _, l := range voiceLanes {
		for _, c := range allChampions() {
			add(l + " " + spokenName(c))
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// voiceGrammarData: the word lists for the structured grammar. Every grammar is a plain sequence of two
// lists of words ("<spell prefix> <champion>"), the simplest structure System.Speech supports.
//
//	prefixes: "flash", "flash de", "cancela flash", "cancela flash de"...
//	game:     spoken forms of this game's champions + lanes (high priority)
//	all:      spoken forms of every champion + lanes (lower weight)
//	names:    every champion name, for "top Darius"
//	flat:     complete phrases for this game only, used if the structured grammar fails to load
type voiceGrammarData struct {
	Prefixes []string `json:"prefixes"`
	Spells   []string `json:"spells"`
	Game     []string `json:"game"`
	All      []string `json:"all"`
	Lanes    []string `json:"lanes"`
	Names    []string `json:"names"`
	Flat     []string `json:"flat"`
	Wake     []string `json:"wake"`
	LanesToo bool     `json:"lanesToo"`
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		x = strings.Join(strings.Fields(x), " ")
		if k := strings.ToLower(x); x != "" && !seen[k] {
			seen[k] = true
			out = append(out, x)
		}
	}
	return out
}

func voiceGrammar(game []string) voiceGrammarData {
	d := voiceGrammarData{Spells: voiceSpells, Lanes: voiceLanes, Flat: append(voicePhrases(game), voiceWake...), Wake: voiceWake}
	for _, sp := range voiceSpells {
		d.Prefixes = append(d.Prefixes, sp, sp+" de", sp+" del", "cancela "+sp, "cancela "+sp+" de", "anula "+sp)
	}
	known := map[string]bool{}
	for _, c := range allChampions() {
		known[c] = true
		d.All = append(d.All, spokenForms(c)...)
		d.Names = append(d.Names, spokenName(c))
	}
	for _, c := range game {
		if known[c] {
			d.Game = append(d.Game, spokenForms(c)...)
		}
	}
	if len(d.Game) > 0 {
		d.Game = append(d.Game, voiceLanes...)
	}
	d.All = append(d.All, voiceLanes...)
	d.Prefixes, d.Game, d.All, d.Names = uniq(d.Prefixes), uniq(d.Game), uniq(d.All), uniq(d.Names)
	return d
}
