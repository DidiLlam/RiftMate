package main

import (
	"sort"
	"strings"
)

// How Spanish/Catalan speakers say champion names, written as a Spanish recognizer would write them, plus the
// usual nicknames and the Spanish client's names. Used twice: the Windows recognizer listens for these forms, and
// the page matches what it heard back to the champion. Your own ways of saying a name (learnt or trained in
// Ajustos) are added to these at run time.
var voiceVariants = map[string][]string{
	"Aatrox": {"Atrox", "Atros", "Aatros"}, "Ahri": {"Ari", "Arri", "Ajri"}, "Akali": {"Acali", "Akali"}, "Akshan": {"Akshan", "Acshan", "Akchan", "Aksan"},
	"Alistar": {"Ali", "Alistar", "Alistair"}, "Ambessa": {"Ambesa", "Ambessa", "Anbesa"}, "Amumu": {"Amumu", "A mumu", "Mumu"},
	"Anivia": {"Anibia", "Anivia", "Pajaro"}, "Annie": {"Ani", "Anni", "Ane"}, "Aphelios": {"Afelios", "Afelio", "Apelios", "Afelius"}, "Ashe": {"Ash", "Ache", "Ashe", "Ach"},
	"Aurelion Sol": {"Aurelion", "Asol", "A sol", "Aurelio Sol", "Aurelion sol", "Dragon espacial"}, "Aurora": {"Aurora", "Orora"}, "Azir": {"Asir", "Azir", "Aser", "Asier"},
	"Bard": {"Bard", "Bardo", "Bar"}, "Bel'Veth": {"Belvez", "Belvet", "Belbet", "Bel vez", "Belveth", "Belbez"}, "Blitzcrank": {"Blitz", "Blitzcran", "Blis", "Blitz cran", "Blizcran"},
	"Brand": {"Bran", "Brand", "Bra"}, "Braum": {"Braum", "Braun", "Bram"}, "Briar": {"Braiar", "Briar", "Brayar", "Brair"},
	"Caitlyn": {"Keitlin", "Caitlin", "Cait", "Keit", "Queitlin", "Kaitlin"}, "Camille": {"Camil", "Kamil", "Camila", "Camille"}, "Cassiopeia": {"Casiopea", "Cassio", "Casio", "Kasiopea"},
	"Cho'Gath": {"Chogat", "Chogath", "Cho gat", "Chogas", "Cogat", "Cho", "Chogaz", "Shogat", "Chogá"}, "Corki": {"Corki", "Korki", "Corqui"},
	"Darius": {"Darius", "Dario", "Darios"}, "Diana": {"Diana", "Daiana"}, "Dr. Mundo": {"Mundo", "Doctor Mundo", "Dr Mundo", "Doctor"},
	"Draven": {"Dreiven", "Draven", "Dreven", "Draben"}, "Ekko": {"Eko", "Eco", "Ecko"}, "Elise": {"Elis", "Elise", "Elisa", "Ilis"}, "Evelynn": {"Evelin", "Eve", "Ebelin", "Evelyn"},
	"Ezreal": {"Ezreal", "Esreal", "Ez", "Es real", "Esrial"}, "Fiddlesticks": {"Fiddle", "Fidel", "Fidelsticks", "Fidel stick", "Fidl", "Fidlesticks"},
	"Fiora": {"Fiora", "Fiora"}, "Fizz": {"Fis", "Fizz", "Fiz", "Fiss"}, "Galio": {"Galio", "Gallo", "Galeo"}, "Gangplank": {"Gangplank", "Gang plank", "Ganplan", "GP", "Gepe", "Ganplank"},
	"Garen": {"Garen", "Garen", "Garren"}, "Gnar": {"Nar", "Gnar", "Ñar", "Guenar"}, "Gragas": {"Gragas", "Grajas", "Grágas"}, "Graves": {"Greivs", "Graves", "Greivz", "Greib"},
	"Gwen": {"Guen", "Gwen", "Wen", "Güen"}, "Hecarim": {"Jecarim", "Hecarim", "Ecarim", "Ecarin", "Caballo"}, "Heimerdinger": {"Heimer", "Jaimer", "Jaimerdinger", "Aimerdinger", "Heimerdinguer", "Donger"},
	"Hwei": {"Juei", "Guei", "Juey", "Uei", "Hwei", "Juai"}, "Illaoi": {"Ilaoi", "Ilaoy", "Ilaui", "Ilayoi"}, "Irelia": {"Irelia", "Irelía", "Ireli"}, "Ivern": {"Aivern", "Ivern", "Ibern", "Aibern"},
	"Janna": {"Yana", "Jana", "Llana", "Ana"}, "Jarvan IV": {"Jarvan", "Yarvan", "Jarvan cuarto", "Yarban", "J4", "Jota cuatro", "Jarban"},
	"Jax": {"Yax", "Jax", "Llax", "Yacs"}, "Jayce": {"Yeis", "Jayce", "Yeys", "Jeis", "Lleis"}, "Jhin": {"Yin", "Jin", "Llin", "Yhin"}, "Jinx": {"Yinx", "Jinx", "Yins", "Llinx", "Yincs"},
	"K'Sante": {"Kasante", "Ksante", "Kesante", "Ca sante", "Ksanti", "Kasanti"}, "Kai'Sa": {"Kaisa", "Caisa", "Kaysa", "Kai sa", "Caisá"}, "Kalista": {"Kalista", "Calista"},
	"Karma": {"Karma", "Carma"}, "Karthus": {"Kartus", "Cartus", "Karthus", "Kartas"}, "Kassadin": {"Kasadin", "Kassadin", "Kasa", "Kass", "Casadin"},
	"Katarina": {"Catarina", "Kata", "Katarina", "Cata"}, "Kayle": {"Keil", "Kail", "Kayle", "Queil", "Keyl"}, "Kayn": {"Kein", "Cain", "Kain", "Quein", "Kayn"},
	"Kennen": {"Kenen", "Kennen", "Quenen"}, "Kha'Zix": {"Kasix", "Kazix", "Kha", "Khazix", "Casix", "Ka six", "Jasix", "Kasics"}, "Kindred": {"Kindred", "Kindre", "Quindred"},
	"Kled": {"Kled", "Cled", "Kle"}, "Kog'Maw": {"Kogmau", "Cogmau", "Kog", "Cog", "Kogmao", "Cog mau", "Kogma"}, "LeBlanc": {"Leblan", "Le blanc", "Leblanc", "Le blan", "Leblang"},
	"Lee Sin": {"Li Sin", "Lee", "Lisin", "Li", "Lee sin", "Li sin"}, "Leona": {"Leona", "Liona"}, "Lillia": {"Lilia", "Lillia", "Lilía"}, "Lissandra": {"Lisandra", "Lissandra", "Lisa"},
	"Locke": {"Lok", "Locke", "Loc"}, "Lucian": {"Lusian", "Lucian", "Lushan", "Lucien"}, "Lulu": {"Lulu", "Lulú"}, "Lux": {"Lux", "Lucs", "Lus"},
	"Malphite": {"Malfait", "Malfite", "Malfaite", "Malfi", "Roca"}, "Malzahar": {"Malzajar", "Malsajar", "Malza", "Malzahar", "Malsa"}, "Maokai": {"Maokai", "Mao", "Maocai", "Maokay"},
	"Master Yi": {"Yi", "Maestro Yi", "Master", "Master Yi", "Mastery", "Maestro"}, "Mel": {"Mel", "Mell"}, "Milio": {"Milio", "Mileo"},
	"Miss Fortune": {"Miss Fortune", "MF", "Eme efe", "Miss", "Misfortun", "Mis fortun"}, "Mordekaiser": {"Morde", "Mordekaiser", "Mordecaiser", "Mordecai", "Mordekaiser"},
	"Morgana": {"Morgana", "Morga"}, "Naafiri": {"Nafiri", "Naafiri", "Na afiri", "Nafri"}, "Nami": {"Nami", "Nam"}, "Nasus": {"Nasus", "Nasos", "Perro"},
	"Nautilus": {"Nautilus", "Naut", "Nautilo", "Nau"}, "Neeko": {"Niko", "Neko", "Nico"}, "Nidalee": {"Nidali", "Nida", "Nidale", "Nidalí"}, "Nilah": {"Nila", "Nilah", "Naila"},
	"Nocturne": {"Nocturne", "Noc", "Noctur", "Nocturn", "Nocturno"}, "Nunu & Willump": {"Nunu", "Nunu y Willump", "Willump", "Nunu i Willump"}, "Olaf": {"Olaf", "Olaff"},
	"Orianna": {"Oriana", "Ori", "Orianna"}, "Ornn": {"Orn", "Ornn", "Or"}, "Pantheon": {"Panteon", "Pantheon", "Panti", "Pante"}, "Poppy": {"Popi", "Poppy", "Popy"},
	"Pyke": {"Paik", "Pike", "Paic", "Payk"}, "Qiyana": {"Kiyana", "Qiyana", "Quiyana", "Kiana"}, "Quinn": {"Kuin", "Quinn", "Cuin", "Quin"}, "Rakan": {"Rakan", "Racan"},
	"Rammus": {"Ramus", "Rammus", "Armadillo"}, "Rek'Sai": {"Reksai", "Rek", "Rec sai", "Reksay", "Recsai"}, "Rell": {"Rel", "Rell", "Rell"}, "Renata": {"Renata", "Renata Glasc", "Renata Glask"},
	"Renekton": {"Renekton", "Rene", "Renecton", "Cocodrilo"}, "Rengar": {"Rengar", "Renga"}, "Riven": {"Riven", "Raiven", "Riben"}, "Rumble": {"Rambel", "Rumble", "Rumbel"},
	"Ryze": {"Rais", "Ryze", "Raiz", "Rise"}, "Samira": {"Samira", "Zamira"}, "Sejuani": {"Seyuani", "Sejuani", "Sejua", "Sejuani", "Sellu"}, "Senna": {"Sena", "Senna"},
	"Seraphine": {"Serafin", "Serafina", "Sera", "Seraphine"}, "Sett": {"Set", "Sett"}, "Shaco": {"Chaco", "Shaco", "Saco"}, "Shen": {"Shen", "Chen", "Sen"},
	"Shyvana": {"Shivana", "Chivana", "Shyvana", "Sivana"}, "Singed": {"Singed", "Singet", "Singuet", "Sinyed"}, "Sion": {"Sion", "Sión", "Saion"}, "Sivir": {"Sibir", "Sivir", "Sivi"},
	"Skarner": {"Skarner", "Escarner", "Scarner", "Escorpion"}, "Smolder": {"Smolder", "Esmolder", "Smoulder", "Esmolde"}, "Sona": {"Sona", "Zona"}, "Soraka": {"Soraka", "Soraca", "Raka"},
	"Swain": {"Suein", "Swain", "Suain", "Suen"}, "Sylas": {"Sailas", "Silas", "Sylas", "Sailes"}, "Syndra": {"Sindra", "Syndra", "Cindra"},
	"Tahm Kench": {"Tam Kench", "Tam", "Tahm", "Tam ken", "Tankench", "Tam kench", "Sapo"}, "Taliyah": {"Talia", "Taliyah", "Talía", "Taliya"}, "Talon": {"Talon", "Talón"},
	"Taric": {"Taric", "Tarik"}, "Teemo": {"Timo", "Teemo", "Temo"}, "Thresh": {"Tresh", "Trech", "Thresh", "Tres", "Tresch"}, "Tristana": {"Tristana", "Trist", "Trista"},
	"Trundle": {"Trundel", "Trundle", "Trondel"}, "Tryndamere": {"Trindamir", "Trinda", "Tryndamere", "Trindamer", "Trynda"},
	"Twisted Fate": {"Tuisted", "TF", "Te efe", "Twisted Fate", "Tuisted feit", "Fate", "Tuistid"}, "Twitch": {"Tuich", "Twitch", "Tuitch", "Rata"},
	"Udyr": {"Udir", "Udyr", "Udi"}, "Urgot": {"Urgot", "Urgo", "Urgod"}, "Varus": {"Barus", "Varus", "Varo"}, "Vayne": {"Vein", "Bein", "Vayne", "Ven"},
	"Veigar": {"Beigar", "Veigar", "Beiga"}, "Vel'Koz": {"Velkoz", "Belkoz", "Vel coz", "Bel coz", "Velcos", "Belcos", "Velkos", "Bel kos", "Velcoz", "Ojo"},
	"Vex": {"Bex", "Vex", "Becs"}, "Vi": {"Vi", "Bi", "Uve i"}, "Viego": {"Biego", "Viego", "Diego"}, "Viktor": {"Biktor", "Viktor", "Victor", "Bictor"},
	"Vladimir": {"Vlad", "Bladimir", "Vladimir", "Blad", "Vladi"}, "Volibear": {"Bolibear", "Voli", "Bolibir", "Volibear", "Oso"}, "Warwick": {"Guaruik", "Warwik", "Warwick", "Uaruik", "Guarguik", "Lobo"},
	"Wukong": {"Bukong", "Gukong", "Wukong", "Ucong", "Wu kong", "Mono"}, "Xayah": {"Chaya", "Shaya", "Xayah", "Saya", "Sayah"}, "Xerath": {"Cherath", "Seraz", "Xerath", "Zerat", "Ceras", "Serath"},
	"Xin Zhao": {"Xin", "Chin Chao", "Sin Chao", "Xin Zhao", "Shin Shao", "Chin"}, "Yasuo": {"Yasuo", "Yaso", "Llasuo", "Yasu"}, "Yone": {"Yone", "Llone", "Yon"},
	"Yorick": {"Iorik", "Yorik", "Yorick", "Llorik"}, "Yunara": {"Yunara", "Llunara"}, "Yuumi": {"Yumi", "Yuumi", "Gato"}, "Zaahen": {"Zaahen", "Sajen", "Zajen", "Saahen"},
	"Zac": {"Sac", "Zac", "Zak"}, "Zed": {"Sed", "Zed", "Zet", "Set"}, "Zeri": {"Seri", "Zeri"}, "Ziggs": {"Sigs", "Ziggs", "Zigs", "Ziks"}, "Zilean": {"Silean", "Zilean", "Zileán", "Ciliean"},
	"Zoe": {"Soe", "Zoe", "Zoé"}, "Zyra": {"Sira", "Zaira", "Zyra", "Saira"},
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
	for _, f := range append(append([]string{spokenName(c)}, voiceVariants[c]...), loadConfig().VoiceAlias[c]...) {
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
