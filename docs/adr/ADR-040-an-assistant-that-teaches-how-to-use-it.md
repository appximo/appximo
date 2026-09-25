# ADR-040 — An assistant that teaches how to use it: a living guide from the schema, a fixed form for creating, corrections on a pending write, names tried against every target, and replies that sound like a person

**Status:** accepted (AGENDA-ASISTENTE-S1, 2026-09-24). Builds on ADR-033/035/037/038/039.
**Drivers:** the agenda's real `/admin/ask` history after two days of daily use
(119 questions: parser 50 %, model 33 %, 15 wasted, US$ 0.14) and a human
sentence bank built from it (151 sentences: 31 verbatim from the history, 19
from the owner's own cases and rows, 101 natural variants — `evidencia/
AGENDA-ASISTENTE-S1/corpus/`) measured against the engine as deployed: **72.8 %
of the intentions hit, 34 % settled by the parser, 64 % paid to the model,
US$ 0.366 per pass, p50 906 ms.** Every creation went to the model; «cómo creo
algo» bought a «no entendí»; a name on a resource with two relations bought
a guess; «no, mejor el viernes» after a confirmation cancelled it and bought
another «no entendí»; and the voice read `16:00`, bullets and emoji names.
The thesis: an assistant that does not teach how to use it forces guessing,
and guessing costs money. Teaching is cheaper than translating.

## Decisions

### 1. The guide is GENERATED from the schema, by levels, with a structure and one complete example each — and self-verified

`ayuda` answers a menu: what the app has (its resources, in the owner's
`summary.resources` order) and the four doors — ask, create, change, the
digest — then the levels to ask for: «cómo creo algo», «qué puedo preguntar»,
«qué campos tiene una tarea», «cómo filtro por fecha», and «más» to continue
any of them (a cursor per identity, ten minutes, `ask.GuideStore`). Each
level answers with a STRUCTURE and ONE full example that can be repeated as
is — «Para crear una tarea di: crear tarea: [qué], área [cuál], persona
[cuál], [urgente], [mañana / el viernes]. Por ejemplo: crear tarea: revisar
el contrato, área casa, persona Fabián Gómez, urgente, el viernes» — with the
fields, states, aliases AND ROWS of this app (a real area, a real person,
read through the caller's own RBAC), so the example works. Nothing is
written by hand: a declared alias, field or resource changes the guide.

**Self-verified**: an example promised as free is parsed on that very schema
before it is shown; one the parser cannot settle is never promised (the
transition example is kept only where the parser resolves the row). Free and
paid are always apart, with the price in the text («≈ US$ 0,003») and in
words in the speech («unos centavos»). A long level is delivered in parts
of at most nine sentences, offering «más». Levels are recognized before the
executable-word check, so «cómo creo una tarea» — which names a resource —
is a guide request, not a create. No domain word lives in the engine.

*Rejected:* a hand-written help text (lies in a month); a list of words
instead of a full sentence (does not teach how to build one); everything at
once (a wall a voice cannot read).

### 2. The fixed form for creating: `crear <recurso>: <qué>, <datos en cualquier orden>`

`crear tarea: arreglar las puertas del auto, área personal, urgente` — a
verb (crear / nueva / anota / agrega / registra…) or the resource word
itself, then WHAT it is, then the data in ANY order, with or without the
field word («área personal» or a bare «personal»), separated by commas, «y»
or the pause dictation leaves. Every datum is recognized by its FORM, never
by a domain word: a field's own name, a declared value or alias, a bool by
its name, a day or a clock, a number with its unit, a name after «con» /
«para», a bare name tried against every target (§4). Whatever is not a datum
is the title, kept as said. Three tolerant cousins settle the sentences the
owner already says: «anota que <lo que pasó>» is the note resource (the one
that records a moment: creatable, no lifecycle, a range without no-overlap or
a time that defaults to now); «anota <infinitivo>…» is a to-do; «compromiso
de 4 a 5 con Fabián hoy» / «reunión con Fabián mañana a las 3 por una hora»
(the agenda word, a clock span, no question word) is a block.

**Chosen over** keyword=value pairs («título: X, área: Y» — exhausting to
dictate), a fixed positional order («crear tarea X Y Z» — has to be
memorized and breaks when one datum is skipped) and the model for everything
(the baseline). Criteria in order: easy to say aloud, easy to remember (one
word: «crear tarea:»), tolerant to order and separators, hard to confuse with
a question (the colon, the infinitive, the clock span). **The confirmation
is unchanged**: the saving is the model call, never the control. Anything the
form cannot settle stays the model's — never a «no entendí» where the model
used to answer (the corpus pins it: zero regressions).

#### 2b. The dictation tail: a verb-less «que …» with a clock span is the note

Seen on the owner's real phone an hour after the deploy: he dictated «anota
que estudio estuvo caído de siete a dos de la tarde» three times and got
«no entendí» three times (US$ 0.0106). The history showed WHY: the sentences
arrived as «que estudio estuvo caído …» — a Siri shortcut named after the
verb («Anota», «Registra») swallows it. So, as a LAST resort after every
shape failed, a sentence that starts with «que», names no resource, carries
no question or operation word and DOES carry a clock span is read as «anota
que …»: the note resource, confirmed like any write (`dictationTail`). A
question that starts with «qué» never reaches it (the reads settle first, or
name a resource, or carry a question word). The clock reader also learned
what a dictation writes: «A.M.»/«P.M.» (one token or two), «7am»/«2pm»
glued, number words in a span («de siete a dos»), «el día de hoy». And the
span rule is written: the end's half of the day is lent to the start («de 7
a 8 de la noche» = 19:00–20:00) unless that runs the span backwards — then
the start keeps its own bare rule («de 7 a 2 de la tarde» = 07:00–14:00).
The owner then asked how a registro should be said at all, so a bank of
sixty human forms of a note (verbed, verb-less, reads) was run on his
schema and every generic gap was closed: past days («ayer», «antier»,
«anoche» = yesterday night), parts of a day that lend their half to a bare
clock («esta mañana de 6 a 7» is 06:00; «esta mañana» used to read as
TOMORROW), a weekday on a note is the PAST one (`last_<weekday>` write
tokens, `ResolveTimeValue`), «entre las 7 y las 2» (the «y» no longer splits
the segment), the approximations «tipo 3» / «como a las 3» / «a eso de las
3», «y cuarto» / «menos cuarto», «7:30» in the create tokenizer (it was a
separator), a second clock later in the sentence as the end («se cayó a las
7 y volvió a las 2»), a colon after a note verb («registra: …»), and the
first-person past of the note verbs naming the note resource on a read
(«qué anoté ayer»). The tail also fires on a past-tense verb with a day
(«que ayer se fue el agua toda la tarde»), not only on a clock span. All of
it is pinned in `TestNote_HumanForms`; a duration with no clock («estuve dos
horas») stays the model's (VOZ-22).

Pinned on the three sentences verbatim; the bank stayed 149/151 with zero
regressions. The cheaper fix on the phone side — a shortcut whose name is
not the verb, so the whole sentence travels — is in the manual, but the
engine tolerates the tail either way.

#### 2c. The assistant speaks «tú», never «vos»; it understands both

The owner asked «why anotá and not anota? I am not Argentinian». The
product's voice had been written in Rioplatense voseo since
VOZ-ESCRITURAS-S1 without anyone deciding it; the real owner is Colombian
and every assistant he uses addresses him as «tú». So every reply — guide,
confirmations, warnings, help, Telegram alerts, the model's prompt — now
speaks neutral «tú» («anota que…», «di más para seguir», «mira el panel»,
«¿confirmas?», «ya tienes…»), and the recognizer keeps BOTH registers: an
accented vos form normalizes to the tú form, and the forms that really
differ («acordate de» / «acuérdate de», «ponelo» / «ponlo», «hacelo» /
«hazlo», «decime» / «dime») are listed in both variants. No knob
(`tu|vos|usted`): it would double hundreds of strings for a preference
nobody asked for; an owner who wants «usted» is the moment for it (VOZ-25).
Decision A-85.

#### 2d. A log looks back; a note «con Nombre» is text with a soft link

The owner's next real questions (2026-09-25): «registros del martes» was
read as a proper name («martes» tried against every relation → «¿cuál?»),
«qué anoté el martes» as the COMING Tuesday, and «registros del 23 de
septiembre» as a name too. A log records what happened, so it has no coming
Tuesday: on a resource whose period target LOOKS BACK — a time range that
does not block (`no_overlap` absent) or the creation stamp — a weekday is the
past one (`last_<weekday>`, strictly before today), a date with no year is
the one that already happened (`past_date:MM-DD`: this year, else the
previous; February 29 → the last leap year), a month with no year the same
(`past_month:MM`, the month in progress counts). The agenda keeps looking
forward (`next_<weekday>`; `date:MM-DD` is the NEXT occurrence, today
included — «agenda X el 3 de enero» said in September is the coming
January); an explicit year is always taken as said. `lookBack` (dates.go) applies the rule to the parser's
plan and to the model's (execute), so both doors agree; the reply names the
day it read («el martes pasado (mar 22 sep)», «el miércoles 23 de septiembre
de 2025»), and the year is spoken in words. No domain word decides it — the
schema's shape does. A date is read as a person says it («el 23 de
septiembre», «23/09», «veintitrés de septiembre», «treinta y uno de
diciembre», «primero de octubre»), and an impossible one («31 de febrero»)
is named at zero cost instead of becoming a name. The same reader serves the
writes («anota que el 23 de septiembre trabajé…» → the note's past date; «para
el 30 de septiembre» → a task's deadline this year). «Qué hice / qué pasó /
qué hicimos» ask the log like «qué anoté».

The same day: «anota que me reuní con Camilo de 4 a 5» died on «No encuentro
«Camilo» como área, compromiso, persona o tarea» — the person was a HARD
reference tried against four targets. On the note, «con Nombre» is the TEXT
of the log and a SOFT reference (`Soft` + `InTitle`): linked when the person
exists, the note written either way with its full text. And a note's text is
kept as said — «se fue la luz», «me llamó el contador» — the leading pronoun
or article is the sentence, not a function word to drop (the other resources
keep dropping «la»/«el» before a title). The dictation tail now counts a
first-person past in «-í» («me reuní», «salí», «escribí») as the past tense
it is, so the verb Siri swallows no longer sends the sentence to the model.

#### 2e. A name that is in no table never kills the write

The owner dictated «anota hablar con Norberto el día de mañana área personal»
and got «No encuentro «Norberto» como área o persona»: on a resource with
TWO nameable relations (areas, personas) a hard reference that matches
nothing refuses the whole create, so a task about a person he had never
loaded was impossible to dictate. A name INSIDE the title is the owner's own
text, so «con Nombre» there is now a SOFT reference that REMEMBERS the words
it took (`Ref.Words`): a known person still becomes the link with the title
unchanged («almuerzo» + Marta), an unknown one puts «con Norberto» back into
the title and the task is written. A name said as its own datum segment
(«crear tarea: revisar, con Marta») stays HARD — there it is a declared
link, and an unknown name deserves the answer, not a title. «El día de
mañana» / «el día de ayer» joined the day phrases on both sides (the read
period and the write token); before, «el día de» stayed in the title.

#### 2f. A noun is a title too, after the SINGULAR word of a work item

«Tarea razón social para óptimo día mañana urgente» (the owner, the same
night) fell to the model — and his credit was out — because the resource-first
create demanded a colon or an INFINITIVE after the resource word («tarea
lavar el carro»). A noun phrase is a title as well, so the singular word now
opens a create when the next word is none of the ones that make a sentence a
QUESTION: a stopword or preposition («tarea de Fabián»), an operation word, a
time word, a clock, or anything the schema knows («tarea pendiente», «tarea
urgente»). It applies only to a WORK ITEM — a resource with a lifecycle or a
time of its own (`isWorkItem`: a state machine, or a non-auto `time` field) —
never to a catalogue of names: the phrase bank caught «persona esposa»
turning into a new person, and «área empresa» with it. Referencing a row by
name and creating a work item by dictating its title are different acts, and
the schema already says which resource is which. Bank: 137/151 hits, parser
131 (was 130), nothing lost.

### 3. A correction on a pending write re-issues the confirmation; it never executes it

«no, mejor el viernes», «mejor a las 5», «sí pero urgente», «que sea con
Marta»: when the words after the lead are exactly data the form recognizes
(a day, a clock, a bool, a declared value, a name, a duration), they are
applied to the pending — a clock keeps the day, a day keeps the clock, a range
keeps its length — and the confirmation is shown AGAIN, saying what changed
(«Cambié vence en.»). ADR-037 §2 stands: a «sí pero…» is never a yes. What
changes is ADR-038 §3's «sí pero mejor el viernes → cancelled, explained»:
that reading was right for a sentence carrying nothing executable; one that
carries a datum is a correction, and cancelling it forced the owner to
dictate the whole order again (measured: X01–X04 of the corpus all cancelled
and three of them bought a model call). Words the form does not recognize
keep the old behavior: cancel, say so, re-read.

### 4. A name the sentence does not place is tried against every candidate target (VOZ-20)

«las tareas de Esposa», «crear tarea: pagar el seguro, Casa», «marca como
hecha la tarea del techo»: on a resource with an area AND a person (and its
own title), the parser used to give up («name could match area_id or
persona_id») and the model was paid to guess. Now the parser says WHICH fields
the name could belong to (`Filter.Fields` for a read, `Plan.Refs` for a
write) and the engine tries each with the same matcher a placed name gets:
relation targets first, the row's own title only when no target holds the
name (a title that merely CONTAINS «Fabián» — «almuerzo con Fabián» — must
never compete with the person). One place → used and said with its kind
(«área: trabajo»); several → the owner picks, each option naming its kind
(«1. área Casa 2. persona Casa»); none → said, naming every kind tried — or,
on a write, a bare word that is nothing anywhere joins the title instead of
refusing the write. And an exact whole token IS the row: «Fabián» is Fabián
Gómez even with a Fabiana around; «Fabi» still asks.

### 5. «Resumen» / «estado» / «gasto» said to the question door are served by it (VOZ-21)

The fixed commands lived in the Telegram receiver; the same words through
`/api/ask` (Siri, the panel) were «no resource named» and a wasted model
call. They are now parser discards at US$ 0, served by the engine's OWN
endpoints called in-process with the caller's identity (`GET /api/summary`,
`?view=census`, `GET /api/ask/spend` — the same digest and the same card the
bot sends, no second implementation; a role that may not see the spend is
told so). «Resumen de tareas» — the summary OF one resource — is its
breakdown by state, a parser read.

### 6. «Ya hice…», «terminé de…», «… está lista» are the finished transition

A first-person "done" phrase moves the named to-do to its FINISHED state:
the terminal state of the machine that is not a cancellation (by the
cancel/anular/rechazar/descartar stems — Spanish verbs, not a domain), when
there is exactly one. The row is named like any transition (§4).

### 7. Replies sound like a person — measured, since no one here can listen

`speech` (and `display`, which is speech plus the cost line when tracing is
on) is composed, not derived: short sentences with a full stop between items;
never a bullet, a guillemet, a pictograph or a raw digit — clocks in words
(«las cuatro de la tarde», «las nueve y media de la mañana»), dates in words
(«el lunes veintiuno de septiembre»), counts in words («dos tareas»), a code
(«ORD-1003») and a money amount kept; a list reads at most FIVE items, then
«y N más; mira el panel» (the screen keeps the page); a confirmation is read
as sentences («Voy a crear una tarea. Título: … Persona: … Vence mañana, el
lunes veintiuno de septiembre a las cuatro de la tarde. ¿Confirmas?»); a
long guide in parts. **Verification:** the 105 has no speech synthesizer and
the agent cannot listen; the criterion is declared and pinned by tests
(`ask.SpeechMetrics`): ≤ 22 words per sentence, ≤ 14 on average, zero digits
and zero symbols in every spoken reply of the corpus, ≤ 5 spoken items. What
Siri itself does with the punctuation (pause length, intonation) is the
shortcut's business: the Speak action honors full stops; if it reads too
fast, the fix is the shortcut's «Rate» setting, not the engine.

### 8. VOZ-15 stays as the written rule; the confirmation says the half of the day in words

A bare hour 1–6 is the afternoon, 7–12 the morning. The corpus evidence: 25
bare hours said in 37 clocks, every one meaning what the rule reads (a
meeting «a las 4» at 16:00, «de 12 a 1» ending at 13:00, «a las 8» at 08:00); the owner's only real compromiso was dictated
qualified («tipo 11 de la mañana»). A declarable working-hours key would add
a knob to decide what nobody has been wrong about; the cheap protection is
that the confirmation now SAYS «a las cuatro de la tarde» in words, so a
wrong reading is heard before anything is written and corrected with «mejor
a las 4 de la mañana» (§3). Reopen VOZ-15 only with a real misread.

## What was measured (the bank, before → after)

The same 151 sentences, the same seed rows, two engines on the 105 (the
deployed `80bd966` and this session's binary), the real model behind both:

| | deployed (`80bd966`) | this session | 
|---|---|---|
| intention hits | 110 / 151 (72.8 %) | **149 / 151 (98.7 %)** — the two misses are sentences the model calls unclear on both engines (T03b, Q15) |
| settled by the parser | 51 (34 %) | **124 (82 %)** |
| model calls per pass | 97 | **21** |
| cost per pass | US$ 0.366 | **US$ 0.083** |
| p50 latency | 906 ms | **10–28 ms** (the model's p50 unchanged, ≈ 1 s) |
| regressions (right before, wrong now) | — | **zero** |
| the 31 verbatim-history sentences | hits 21, model 17, US$ 0.061 | hits 29, model 7, US$ 0.025 |

Projection from the owner's real rate (58 questions/day, US$ 0.070/day on
the deployed engine): **US$ 2.09 → 0.86 per month**; and of the 21 model calls
left in a pass, 7 are loose creates that the fixed form settles for free —
following the guide's own form takes the month to ≈ US$ 0.57. The guide's
effect measured directly: every example the guide shows (44 on the agenda,
13 on the 20-resource conjunto, 11 on the English quickstart) fired back at
the engine answers `source: parser`, US$ 0. Fifteen provocations (one per
scenario of the brief, `evidencia/AGENDA-ASISTENTE-S1/provocations.log`)
15/15; over their 80 spoken replies: zero digits, zero symbols, zero
pictographs, at most 22 words in a sentence composed by the engine (28 in
one the model wrote).

## What is deliberately not built (registered)

- A sentence with two intentions executes ONE: the first is planned, the
  second is said back («dímelo aparte cuando confirmes») — a queue of
  pendings would let a stray yes execute the wrong one (VOZ-23).
- Relative times («en una hora», «dentro de dos días») stay the model's:
  the token vocabulary is closed on purpose (ADR-037 §6) (VOZ-22).
- A verb of beginning («arrancá con», «empezá») is not mapped to the
  in-progress state: it names no state, and one sentence in the bank is not
  evidence enough for a word list (VOZ-24).
- Working hours are not declarable (§8, VOZ-15 closed); recurrences are v2
  (ADR-039).
- A synthesized listening test: no engine on the box, no agent ear; the
  metric stands in, and the human check is VOZ-6.
