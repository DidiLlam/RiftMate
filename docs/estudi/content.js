// The course: lessons, general questions, and the questions made from your own games (by the kind of mistake).
// Objective timers: patch 26.21 (Baron back to 20:00 and Atakhan removed in 26.1); sources in SOURCES.
window.SOURCES = [
  ["Riot: /dev 2026 Season One Gameplay Preview", "https://www.leagueoflegends.com/en-us/news/dev/dev-2026-season-one-gameplay-preview/"],
  ["BoostPirate: LoL objective timers (pedaç 26.21)", "https://boostpirate.com/guides/lol-objective-timers"],
  ["NeuralBoost: objective timers 2026", "https://neuralboost.gg/blog/league-of-legends-objective-timers-2026-dragon-baron-herald-voidgrubs"],
  ["LoL Theory: wave management", "https://blog.loltheory.gg/wave-management-league-of-legends/"],
  ["Dignitas: wave management guide", "https://dignitas.gg/articles/wave-management-guide-for-lol"]
];

// map marks: x,y from 0 to 100 (your base bottom-left). kind: dot, ward, arrow (x2,y2), pit, zone (r), label
window.LESSONS = [
{id: "onades", name: "Les onades", short: "Empènyer, congelar, rebotar",
 intro: "L'onada de minions decideix on es juga la línia: qui pot moure's, qui perd farm i on et poden matar. Controlar-la és el primer pas de la macro.",
 sections: [
  ["Cap a on va l'onada", "L'onada amb més minions empeny. Si mates els minions rivals més de pressa que ell els teus, l'onada avança cap a la seva torre. Quan xoca a una torre, la torre la desfà i al cap d'una estona torna cap a tu sola: és el rebot."],
  ["Empènyer ràpid (fast push)", "Mates l'onada tan de pressa com pots perquè xoqui a la torre rival. És el que fas abans de tornar a base, de moure't a una altra línia o d'anar a un objectiu: mentre la torre es menja l'onada, el rival no pot seguir-te sense perdre farm."],
  ["Empènyer lent (slow push)", "Mates només els minions de rang (els del darrere) i deixes que la teva onada creixi durant 2 o 3 onades. Quan xoca és enorme: perfecta abans d'un drac, per atacar la torre o per tornar a base deixant una onada gran al rival."],
  ["Congelar (freeze)", "Mantens l'onada a prop de la teva torre sense que hi arribi, fent només l'últim cop. Per fer-ho cal que l'onada rival tingui uns quants minions més que la teva. El rival ha d'avançar molt per farmejar i queda a mercè del teu jungla. Va bé quan vas per davant o quan el rival no té com escapar."],
  ["L'onada del canó", "És la que val més or. No la perdis mai, i si pots, fes que xoqui la del canó abans de tornar a base."]
 ],
 rules: ["Abans de moure't o de tornar a base, empeny.", "No empenyis fins a la torre rival si no saps on és el jungla rival: és on et poden matar.", "Si vas per darrere, deixa que l'onada vingui cap a tu.", "Una onada a prop de la teva torre et protegeix; una onada a la seva et deixa temps lliure."],
 errors: ["Empènyer sense visió i morir al riu.", "Tornar a base amb una onada gran que va cap a la teva torre: la perds.", "Pegar al rival dins de la seva onada: els minions fan molt mal al principi."],
 map: [{k: "zone", x: 50, y: 50, r: 8, t: "onada"}, {k: "arrow", x: 46, y: 54, x2: 62, y2: 38, t: "empènyer"}, {k: "dot", x: 66, y: 34, t: "torre rival"}, {k: "dot", x: 34, y: 66, t: "la teva torre"}]},

{id: "tornar", name: "Tornar a base", short: "Quan i amb quant",
 intro: "Cada tornada a base és una decisió: quanta onada perds, què compres i quan arribes al proper objectiu.",
 sections: [
  ["Quan tornar", "El millor moment és just després que la teva onada xoqui a la torre rival: mentre tu ets a base, la torre se la menja i el rival no guanya res. També quan tens or per a un component important, quan et falta vida o manà, o un minut abans d'un objectiu per arribar-hi ple."],
  ["Quan no tornar", "Amb una onada gran que va cap a la teva torre (la perds o te la menja la torre), just quan surt un objectiu que vols jugar, o amb or que no compra res útil."],
  ["El cicle", "Empeny, torna, i arriba a la línia quan l'onada encara és al mig. Si el teu jungla va a fer un objectiu, intenta tornar a la vegada que ell per arribar-hi plegats."],
  ["Què comprar", "Un component que et doni força ara val més que guardar or. Un ward de control a cada tornada: és el que més visió dona pel que costa."],
  ["Quan torna el rival", "Si el rival torna i tu no, empeny la teva onada cap a la seva torre: la perd mentre és a base."]
 ],
 rules: ["Empeny, i després torna.", "Torna amb or per a alguna cosa que canviï els teus combats.", "Sincronitza les tornades amb els objectius."],
 errors: ["Tornar a base amb poca vida però amb l'onada al teu costat: perds 2 onades.", "Quedar-te a la línia amb molt d'or i morir amb tot l'or a sobre.", "Tornar just quan surt el drac."],
 map: [{k: "dot", x: 8, y: 8, t: "base"}, {k: "arrow", x: 52, y: 48, x2: 12, y2: 12, t: "torna"}, {k: "zone", x: 64, y: 36, r: 6, t: "onada a la torre"}]},

{id: "objectius", name: "Objectius", short: "Timers 2026 i prioritat",
 intro: "Els monstres èpics i les torres guanyen partides. Saber quan surten i preparar-los un minut abans és el més rendible de la macro.",
 sections: [
  ["Quan surten (pedaç 26.21)", "Drac: al 5:00, i cada 5:00 després de matar-lo, fins que un equip en té quatre (ànima). Drac ancestral: 6:00 després del quart. Larves del Buit: al 8:00, un sol cop (marxen al 14:45). Herald: al 15:00, un sol cop (marxa al 19:45). Baró: al 20:00, i 6:00 després de matar-lo. Escarabat del riu: al 2:55, i 2:30 després."],
  ["Prioritat", "Té prioritat l'equip que té les línies del costat de l'objectiu empeses: pot arribar-hi primer sense perdre res. Un minut abans: el mid empès, wards al voltant i, si cal, tornar a base per arribar-hi ple."],
  ["Intercanvis", "Si no podeu disputar un objectiu, feu-ne un altre a l'altra punta: larves o herald per un drac, una torre per un drac. Perdre'l sense obtenir res és el pitjor."],
  ["No moris abans", "Morir un minut abans d'un objectiu és regalar-lo: el rival hi va amb un més."],
  ["Torres i plaques", "Des del 2026 les plaques no cauen al minut 14: es queden tota la partida (i n'hi ha també a les torres de dins). Una onada gran a la torre rival sempre treu or."]
 ],
 rules: ["Un minut abans: empeny el mid i posa visió.", "Si no pots disputar-lo, canvia'l per un altre.", "No et moris abans de l'objectiu."],
 errors: ["Arribar al drac sense haver empès el mid: el rival arriba primer.", "Lluitar el drac en inferioritat perquè un company ha mort abans.", "Deixar passar el Baró al 20:00 sense ni mirar-lo."],
 map: [{k: "pit", x: 70, y: 70, t: "Baró / Herald / larves"}, {k: "pit", x: 30, y: 30, t: "Drac"}, {k: "ward", x: 38, y: 34}, {k: "ward", x: 32, y: 40}, {k: "arrow", x: 50, y: 50, x2: 34, y2: 34, t: "del mid al drac"}]},

{id: "roam", name: "Moure't des del mid", short: "Quan i on fer roam",
 intro: "Des del mid tens el camí més curt a totes les línies. Un bon roam guanya la partida; un de dolent et deixa sense farm i sense experiència.",
 sections: [
  ["Quan", "Quan la teva onada ha xocat a la torre rival, saps on és el jungla rival i la línia on vas té el rival endavant (lluny de la seva torre) o sense Flash."],
  ["On", "On hi ha més possibilitats: una línia amb l'onada empesa cap a la vostra torre (el rival és lluny de la seva), a bot hi pot haver dues kills, i amb el teu jungla a prop encara més."],
  ["El preu", "Cada onada que no recull et costa or i experiència. Si el roam no surt, has perdut tu. Per això primer empeny: així la torre rival es menja la teva onada mentre ets fora."],
  ["Si el rival mid desapareix", "Avisa (ping) i decideix: el segueixes pel mateix camí o empenys i treus plaques de la seva torre."],
  ["Altres maneres de moure't", "Ajudar el jungla a l'escarabat del riu (2:55), invadir amb ell o posar visió profunda també és roam."]
 ],
 rules: ["Primer empeny, després mou-te.", "Ves on el rival està lluny de la seva torre.", "Si el rival desapareix, avisa i fes alguna cosa: seguir-lo o treure plaques."],
 errors: ["Moure't amb l'onada al teu costat: la perds sencera.", "Anar a una línia on el rival és sota la seva torre.", "No fer res quan el rival mid fa roam."],
 map: [{k: "dot", x: 50, y: 50, t: "mid"}, {k: "arrow", x: 50, y: 50, x2: 22, y2: 78, t: "a dalt pel riu"}, {k: "arrow", x: 50, y: 50, x2: 78, y2: 22, t: "a baix pel riu"}]},

{id: "visio", name: "Visió i el jungla rival", short: "Wards i on és el jungla",
 intro: "La majoria de morts de la fase de línies són ganks que es podien veure venir. Saber on és el jungla rival val més que qualsevol ward.",
 sections: [
  ["On ha començat", "Mira on apareix als primers minuts o qui li ha fet ajuda al primer camp. Si fa tota la jungla d'un costat a l'altre, el primer gank arriba cap al minut 3 a la banda on acaba; si ha començat a la teva banda, pot arribar abans."],
  ["Wards", "Posa el ward groc al riu abans que el jungla hi pugui arribar, no quan ja hi és. Un ward de control a l'arbust del riu per on ve el jungla et protegeix molta estona. Si no veus el jungla, suposa que és a prop teu."],
  ["Llums de fada (2026)", "Hi ha punts fixos al mapa (llums de fada) on un ward veu més lluny del normal: aprofita'ls."],
  ["Abans dels objectius", "Un minut abans, wards al voltant del drac o del Baró, i la lent per treure els seus."],
  ["Comptar", "Mira el mapa cada pocs segons: quants rivals veus i on. Si en falten tres, no avancis."]
 ],
 rules: ["Ward abans, no quan ja hi és.", "Si no el veus, és a prop teu.", "Sense visió, no passis de la meitat del riu."],
 errors: ["Empènyer sense ward i morir pel jungla.", "Gastar el ward al principi i no tenir-ne quan arriba el gank.", "No comprar mai wards de control."],
 map: [{k: "ward", x: 42, y: 42}, {k: "ward", x: 58, y: 58}, {k: "ward", x: 34, y: 56}, {k: "ward", x: 56, y: 34}, {k: "dot", x: 74, y: 52, t: "jungla rival?"}]},

{id: "migjoc", name: "Mig joc", short: "El mapa després de les línies",
 intro: "Quan cauen les primeres torres, el joc canvia: ja no hi ha línies fixes, i es guanya agrupant-se bé i agafant les onades del costat sense morir-hi.",
 sections: [
  ["Onades del costat", "Algú ha d'anar a buscar les onades de dalt i de baix. Ha de ser qui pot sobreviure sol o escapar, i ha d'arribar-hi quan no hi ha cap objectiu a punt."],
  ["Agrupar", "Per disputar un objectiu, tot l'equip hi ha de ser. Recull l'onada del costat abans, no mentre l'equip lluita sense tu."],
  ["Comptar rivals", "Si veus els cinc rivals al mapa, pots avançar tranquil. Si en falten, et poden estar esperant: no entris a la jungla rival sense visió."],
  ["Morir sol", "Morir sol al costat del mapa quan falten rivals és el que més partides perd al mig joc: el rival agafa un objectiu en superioritat."]
 ],
 rules: ["Agafa les onades del costat abans dels objectius.", "Si no veus els rivals, no avancis sol.", "Agrupa't amb l'equip per lluitar."],
 errors: ["Anar sol a una línia del costat quan falten 3 rivals.", "Farmejar mentre l'equip lluita el Baró.", "Entrar a la jungla rival sense visió per fer una kill."],
 map: [{k: "zone", x: 20, y: 80, r: 7, t: "onada de dalt"}, {k: "zone", x: 50, y: 50, r: 7, t: "equip"}, {k: "dot", x: 70, y: 70, t: "Baró"}]},

{id: "avantatge", name: "Per davant i per darrere", short: "Aprofitar i remuntar",
 intro: "Anar per davant no guanya res sol: cal convertir-ho en torres i objectius. Anar per darrere tampoc és perdut: els objectius donen remuntades.",
 sections: [
  ["Quan vas per davant", "Converteix l'avantatge: plaques, torres, objectius i visió a la jungla rival. No persegueixis kills dins de la boira ni vagis sol: tens una recompensa (bounty) i, si et maten, el rival remunta."],
  ["Quan vas per darrere", "Farmeja segur, defensa les torres amb les onades, posa visió a la teva jungla i espera un error. Lluita a prop de les teves torres. Els monstres èpics donen més experiència a l'equip que va per darrere (2026)."],
  ["Tancar la partida", "Amb el Baró, empeny dues línies a la vegada en lloc d'entrar els cinc per una. Després de treure un inhibidor, torna a base i torna a sortir junts."],
  ["Recompenses", "Un rival amb ratxa de kills val molt d'or si el mates. Al revés: si tu en tens, no arrisquis."]
 ],
 rules: ["Avantatge = objectius, no kills.", "Per darrere: farm segur i lluitar a prop de casa.", "Amb el Baró, empeny per dues línies."],
 errors: ["Anar +3.000 d'or i perdre per baralles soles.", "Entrar a la base rival amb pocs minions.", "Perseguir una kill i perdre el Baró."],
 map: [{k: "arrow", x: 22, y: 30, x2: 22, y2: 72, t: "onada 1"}, {k: "arrow", x: 30, y: 22, x2: 72, y2: 22, t: "onada 2"}, {k: "dot", x: 88, y: 88, t: "base rival"}]}
];

// general questions: l = lesson, a = index of the right option
window.QUESTIONS = [
 {id: "o1", l: "onades", q: "Vols tornar a base. L'onada és al mig i la següent està a punt d'arribar. Què fas?", o: ["Empenyo l'onada fins a la torre rival i torno", "Torno ara mateix", "Espero que l'onada vingui a la meva torre", "Vaig a la jungla a farmejar"], a: 0, why: "Si empenys abans, la torre rival es menja la teva onada mentre ets a base i no perds farm."},
 {id: "o2", l: "onades", q: "Per congelar l'onada a prop de la teva torre cal que…", o: ["l'onada rival tingui uns quants minions més que la teva", "tinguis més minions que el rival", "pegues a la torre", "matis tots els minions de cop"], a: 0, why: "Amb més minions rivals, l'onada empeny cap a tu i, fent només l'últim cop, es queda quieta davant de la teva torre."},
 {id: "o3", l: "onades", q: "Per a què serveix l'empènyer lent (slow push)?", o: ["Per fer una onada molt gran abans d'un objectiu o d'atacar la torre", "Per farmejar més segur", "Per congelar l'onada", "Per guanyar experiència més de pressa"], a: 0, why: "Deixant créixer l'onada 2 o 3 onades, quan xoca és tan gran que el rival hi perd molt o tu pots atacar la torre."},
 {id: "o4", l: "onades", q: "Vas per davant i el rival no té Flash. Què et convé fer amb l'onada?", o: ["Congelar-la a prop de la meva torre", "Empènyer-la a la seva torre", "Deixar-la anar", "Res"], a: 0, why: "Congelant, el rival ha d'avançar per farmejar i queda exposat a tu i al teu jungla."},
 {id: "o5", l: "onades", q: "Quina onada no has de perdre mai?", o: ["La del canó", "La primera", "La que va cap a la teva torre", "Totes valen igual"], a: 0, why: "El canó val molt més or que un minió normal."},
 {id: "o6", l: "onades", q: "Empenys l'onada fins a la torre rival i no saps on és el jungla rival. Quin és el risc?", o: ["Que et faci un gank lluny de la teva torre", "Que perdis farm", "Cap", "Que la torre et mati"], a: 0, why: "Amb l'onada a la seva torre ets lluny de la teva: és el moment en què un gank et mata."},
 {id: "t1", l: "tornar", q: "Quin és el millor moment per tornar a base?", o: ["Just després que la teva onada xoqui a la torre rival", "Quan tens l'onada a la teva torre", "Quan surt el drac", "Quan el rival torna"], a: 0, why: "Mentre ets a base, la torre rival es menja l'onada i no perds res."},
 {id: "t2", l: "tornar", q: "El rival torna a base i tu no pots. Què fas?", o: ["Empenyo la meva onada cap a la seva torre", "Torno jo també", "Vaig a la jungla", "Em quedo quiet"], a: 0, why: "Mentre ell és fora, la torre es menja la seva onada i perd or i experiència."},
 {id: "t3", l: "tornar", q: "Falta 1 minut perquè surti el drac i tens poca vida. Què et convé?", o: ["Empènyer el mid i tornar ara per arribar-hi ple", "Quedar-me a la línia", "Anar al drac així", "Esperar que surti i després tornar"], a: 0, why: "Tornant un minut abans, arribes al drac amb vida, manà i objectes nous."},
 {id: "t4", l: "tornar", q: "Què és el més rendible de comprar a cada tornada, a més dels objectes?", o: ["Un ward de control", "Pocions", "Botes", "Res"], a: 0, why: "Un ward de control dona visió molta estona i treu els wards rivals."},
 {id: "b1", l: "objectius", q: "A quin minut surt el primer drac (pedaç 26.21)?", o: ["5:00", "8:00", "15:00", "20:00"], a: 0, why: "El primer drac surt al 5:00 i el següent 5 minuts després de matar-lo."},
 {id: "b2", l: "objectius", q: "A quin minut surt el Baró (des del 2026)?", o: ["20:00", "25:00", "15:00", "30:00"], a: 0, why: "Al pedaç 26.1 el Baró va tornar al 20:00, quan van treure l'Atakhan."},
 {id: "b3", l: "objectius", q: "Quan surten les larves del Buit?", o: ["Al 8:00, un sol cop", "Al 5:00 i cada 4 minuts", "Al 15:00", "Al 2:55"], a: 0, why: "Surten al 8:00 un sol cop i marxen al 14:45 si ningú les fa."},
 {id: "b4", l: "objectius", q: "Quan surt l'herald?", o: ["15:00", "8:00", "20:00", "14:00"], a: 0, why: "Surt al 15:00 un sol cop i marxa al 19:45."},
 {id: "b5", l: "objectius", q: "Què has de fer un minut abans d'un objectiu?", o: ["Empènyer el mid i posar visió al voltant", "Farmejar a la jungla", "Anar a una línia del costat", "Res, esperar que surti"], a: 0, why: "Amb el mid empès arribes primer, i amb visió saps si el rival hi va."},
 {id: "b6", l: "objectius", q: "El rival té prioritat al drac i vosaltres no hi podeu arribar. Què feu?", o: ["Anem a fer un altre objectiu a l'altra banda", "Hi anem igualment", "Ens quedem a la base", "Res"], a: 0, why: "Canviar objectius (larves, herald o una torre per un drac) és millor que perdre'l sense res a canvi."},
 {id: "b7", l: "objectius", q: "Les plaques de les torres, al 2026…", o: ["es queden tota la partida", "cauen al minut 14", "no existeixen", "cauen al minut 20"], a: 0, why: "Des del 2026 les plaques no cauen: es queden tota la partida i també n'hi ha a les torres de dins."},
 {id: "r1", l: "roam", q: "Quan és un bon moment per fer roam des del mid?", o: ["Quan la meva onada ha xocat a la torre rival", "Quan tinc l'onada a la meva torre", "Quan el rival és a la línia amb tota la vida i l'onada al seu costat", "Mai"], a: 0, why: "Amb l'onada a la torre rival, el rival no et pot seguir sense perdre-la."},
 {id: "r2", l: "roam", q: "El mid rival desapareix. Què fas?", o: ["Aviso i el segueixo o trec plaques de la seva torre", "Res", "Torno a base", "Vaig a la jungla rival sol"], a: 0, why: "Cal avisar i aprofitar-ho: o arribes al mateix lloc, o li fas pagar el roam amb plaques."},
 {id: "r3", l: "roam", q: "A quina línia té més sentit fer roam?", o: ["On el rival és lluny de la seva torre", "On el rival és sota la seva torre", "La més lluny", "On el meu company va per darrere i és a la torre"], a: 0, why: "Un rival endavant no té on refugiar-se quan arribes."},
 {id: "r4", l: "roam", q: "Què perds quan fas roam sense empènyer?", o: ["L'onada sencera: or i experiència", "Res", "Només vida", "Els wards"], a: 0, why: "L'onada arriba a la teva torre i se la menja la torre o el rival."},
 {id: "v1", l: "visio", q: "Quan has de posar el ward al riu?", o: ["Abans que el jungla rival hi pugui arribar", "Quan el veig", "Al minut 10", "Quan tinc poca vida"], a: 0, why: "Un ward posat tard no t'avisa de res: el jungla ja és a sobre."},
 {id: "v2", l: "visio", q: "No veus el jungla rival des de fa estona. Què has de suposar?", o: ["Que és a prop meu", "Que farmeja a l'altra banda", "Que és a base", "Res"], a: 0, why: "Suposar el pitjor et fa jugar segur; si t'equivoques, no perds res."},
 {id: "v3", l: "visio", q: "Si el jungla rival comença a la banda de baix i fa tota la jungla, on és més probable el primer gank cap al minut 3?", o: ["A la banda de dalt", "A bot", "Al mid sempre", "Enlloc"], a: 0, why: "Acaba la neteja a l'altra punta (dalt) i hi és a prop per ganquejar."},
 {id: "v4", l: "visio", q: "Què són les llums de fada (2026)?", o: ["Punts fixos on un ward veu més lluny", "Un objectiu nou", "Un tipus de minió", "Un objecte"], a: 0, why: "Al 2026 hi ha punts fixos al mapa on un ward dona més visió."},
 {id: "m1", l: "migjoc", q: "Falten 3 rivals al mapa i tu ets sol a la línia de dalt. Què fas?", o: ["Recullo l'onada a prop de la meva torre o me'n vaig cap a l'equip", "Empenyo fins a la seva torre", "Entro a la seva jungla", "Res"], a: 0, why: "Si no saps on són, et poden estar esperant: morir sol aquí regala un objectiu."},
 {id: "m2", l: "migjoc", q: "Quan has d'anar a buscar una onada del costat?", o: ["Quan no hi ha cap objectiu a punt de sortir", "Mentre l'equip lluita el Baró", "Just quan surt el drac", "Mai"], a: 0, why: "Recull-la abans dels objectius, no quan l'equip et necessita."},
 {id: "m3", l: "migjoc", q: "Qui ha d'anar a les onades del costat al mig joc?", o: ["Qui pot sobreviure sol o escapar", "El suport", "El que té menys vida", "Ningú"], a: 0, why: "Ha de poder aguantar o fugir si li vénen dos rivals."},
 {id: "a1", l: "avantatge", q: "Vas +3.000 d'or. Què has de fer?", o: ["Convertir-ho en torres, objectius i visió", "Buscar més kills a la jungla rival", "Farmejar sol", "Res"], a: 0, why: "L'avantatge s'ha de convertir en coses que no es perden: torres i objectius."},
 {id: "a2", l: "avantatge", q: "Aneu per darrere. Què et convé?", o: ["Farm segur, defensar amb les onades i lluitar a prop de casa", "Lluitar a la jungla rival", "Anar sol a buscar kills", "Rendir-se"], a: 0, why: "Esperant un error a prop de casa, les baralles et són favorables."},
 {id: "a3", l: "avantatge", q: "Teniu el Baró. Com empenyeu?", o: ["Per dues línies a la vegada", "Tots cinc per una línia sense minions", "Cadascú per una línia", "No empenyem"], a: 0, why: "Dues onades amb el Baró obliguen el rival a dividir-se."},
 {id: "a4", l: "avantatge", q: "Tens una ratxa de kills (recompensa alta). Què has de fer?", o: ["No arriscar: si et maten, el rival remunta molt", "Buscar més kills", "Anar sol a les bandes", "Res"], a: 0, why: "La recompensa que val la teva mort ajuda molt el rival a remuntar."}
];

// questions from your own games: one per kind of mistake. e = the example from the PC: d date, c champion,
// m minute, z place, e enemies near, a allies near, x extra
const where = z => !z ? "" : /^(la |les )/.test(z) ? ` a ${z}` : ` al ${z.replace(/^base /, "base ")}`;
window.PERSONAL = {
 gank: {l: "visio", q: e => `El ${e.d}, amb ${e.c}, al minut ${e.m}, et va matar un gank del jungla. Què t'hauria salvat?`,
  o: ["Saber on havia començat el jungla i jugar segur a partir del minut 3", "Tenir més vida", "Pegar més al rival", "Res: els ganks no es poden preveure"],
  why: "Seguint on comença el jungla i quan acaba la neteja, saps quan pot arribar. Sense aquesta informació, sense ward, no passis de la meitat del carril."},
 endins: {l: "visio", q: e => `El ${e.d}, amb ${e.c}, al minut ${e.m}, vas morir${where(e.z)} sense cap ward posat els 3 minuts abans (${e.e || "uns quants"} rivals a prop i ${e.a || 0} aliats). Què hauries d'haver fet?`,
  o: ["No entrar-hi sense visió, o posar un ward abans d'entrar", "Entrar-hi més de pressa", "Anar-hi amb menys vida", "Res, era mala sort"],
  why: "Entrar al riu o a la jungla rival sense visió és jugar a cegues: amb un ward abans hauries vist venir els rivals."},
 sol: {l: "migjoc", q: e => `El ${e.d}, amb ${e.c}, al minut ${e.m}, vas morir${where(e.z)} amb ${e.e} rivals a prop i només ${e.a} aliats. Què va fallar?`,
  o: ["Estar sol en un lloc on podien arribar més rivals sense saber on eren", "Tenir pocs objectes", "No tenir Flash", "Res"],
  why: "Al mig joc, si falten rivals al mapa, no et quedis sol lluny de l'equip: et caçaran en inferioritat."},
 preobj: {l: "objectius", q: e => `El ${e.d}, amb ${e.c}, vas morir al minut ${e.m} i poc després el rival va fer ${e.x || "un objectiu"}. Què hauries d'haver fet?`,
  o: ["No arriscar el minut abans de l'objectiu: empènyer i anar-hi amb l'equip", "Anar sol a l'objectiu", "Farmejar a l'altra banda", "Res, l'objectiu no importava"],
  why: "Morir just abans d'un objectiu és regalar-lo: el rival hi va amb un més."},
 objs: {l: "objectius", q: e => `El ${e.d}, amb ${e.c}, el rival va fer molts més objectius que vosaltres (${e.x}, vosaltres-ells). Què us va faltar?`,
  o: ["Preparar-los un minut abans: mid empès i visió", "Més kills", "Més farm", "Res"],
  why: "Els objectius es guanyen abans que surtin: prioritat a les línies i visió al voltant."},
 farm: {l: "onades", q: e => `El ${e.d}, amb ${e.c}, vas fer ${String(e.x).replace(".", ",")} de farm per minut. Què el puja més?`,
  o: ["Tornar a base després d'empènyer i anar a buscar les onades del costat quan no hi ha res a fer", "Fer més roams sense empènyer", "Quedar-me a la línia sense tornar mai", "Pegar més al rival"],
  why: "El farm es perd sobretot en tornades mal fetes i en estones mortes del mig joc: les onades del costat sempre són or."},
 kp: {l: "roam", q: e => `El ${e.d}, amb ${e.c}, vas tenir un ${e.x} de participació en kills. Què la puja?`,
  o: ["Moure'm quan l'onada xoca a la torre rival i ajudar on hi ha acció", "Quedar-me sempre a la línia", "Anar sol a la jungla rival", "Res"],
  why: "Després d'empènyer tens temps lliure: fes-lo servir al riu, a l'escarabat o a la línia del costat."},
 llencat: {l: "avantatge", q: e => `El ${e.d}, amb ${e.c}, anàveu ${e.x} d'or al minut 15 i vau perdre. Què us va faltar?`,
  o: ["Convertir l'avantatge en torres i objectius en grup", "Més kills", "Esperar que el rival s'equivoqués", "Res"],
  why: "Un avantatge que no es converteix en objectius es perd en baralles soles."},
 torre: {l: "onades", q: e => `El ${e.d}, amb ${e.c}, al minut ${e.m}, et va matar la torre o els minions. Què va fallar?`,
  o: ["Entrar sota torre o dins l'onada rival sense que els meus minions aguantessin els cops", "No tenir prou mal", "No tenir Flash", "Res"],
  why: "Sota torre, la torre ha d'apuntar als teus minions; i dins l'onada rival, els seus minions et fan molt mal."}
};
window.KIND_NAME = {gank: "morts per gank abans del 14", endins: "morts al riu o a la jungla rival sense ward", sol: "morts sol en inferioritat (mig joc)", preobj: "morts just abans d'un objectiu del rival", objs: "3 objectius o més de diferència en contra", farm: "menys de 7 de farm per minut", kp: "menys del 45% de participació en kills", llencat: "perdudes anant +2.000 d'or al 15", torre: "morts per la torre o els minions"};
