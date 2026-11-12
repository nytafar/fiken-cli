# Fiken CLI

English version: [README.md](README.md)

Verktøy for å føre regnskap i Fiken sammen med en AI-agent. Du tar avgjørelsene: hvilken
termin som skal lukkes, hvilken kvittering som hører til hvilket trekk, og når meldingen
leveres. Agenten gjør grovarbeidet gjennom `fiken-cli` og to skills, og alt den skriver blir
kontrollert mot banklinjen og logget.

Verktøyet har tre deler:

- **Et lokalt speil av regnskapet.** Tolv ressurser per selskap synkes til SQLite. Spørsmål
  om historikk besvares lokalt, med SQL og fritekstsøk, i stedet for gjennom et API med
  strenge grenser.
- **Kontroller og oversikter oppå speilet.** Øreavvik, MVA-koder som bryter med mønsteret
  for en leverandør, dobbeltføringer, manglende bilag, en oppstilling formet som
  MVA-meldingen, og omsetning, kostnad og margin per periode.
- **Bankavstemming i Superføring.** Fikens eget avstemmingspanel har ikke noe API. Skillen
  `fiken-superforing` styrer panelet i nettleseren din mens du ser på: den leser de åpne
  linjene, bokfører betalte kjøp fra dem med bilaget vedlagt, og bekrefter dem.

## Hva du bruker det til

| Du vil | Si til agenten | Den bruker |
| --- | --- | --- |
| Vite hvilke banklinjer som står åpne, og hva som mangler bilag | «Hva står åpent i banken for juli og august?» | `fiken-superforing`, kartlegging: leser og rapporterer, skriver ingenting |
| Bokføre det banken viser | «Bokfør og bekreft de linjene» | `fiken-superforing`: ett betalt kjøp per linje, bilag vedlagt, linjen bekreftet |
| Finne feil før en MVA-melding | «Gå gjennom juli og august for feil» | `drift`, `vat-anomaly`, `duplicates`, `missing-bilag` |
| Se MVA-beløpet før du leverer | «Hva kommer meldingen for terminen til å vise?» | `mva-summary`, avstemt mot hovedboken |
| Se hvordan en leverandør pleier å føres | «Hvordan fører vi denne leverandøren?» | `vendor-profile`, speilet |
| Se omsetning, kostnad eller margin | «Margin per måned i år» | `rollup` |

MVA-meldingen leverer du selv i Fiken. Verktøyet gjør regnskapet klart til det.

## Installasjon

```bash
curl -fsSL https://raw.githubusercontent.com/nytafar/fiken-cli/main/install.sh | bash
npx skills add nytafar/fiken-cli --skill '*' --agent claude-code --global
```

Første linje installerer siste utgitte versjon i `~/.local/bin` og kontrollerer sjekksummen.
Andre linje installerer begge skills, `fiken` og `fiken-superforing`, gjennom
[`npx skills`](https://skills.sh/).

Skillene i dette repoet beskriver `fiken-cli` 2.2.0. Sjekk med `fiken-cli version`. Ga
installasjonen deg en eldre versjon, bygger du fra en klone:

```bash
git clone https://github.com/nytafar/fiken-cli && cd fiken-cli
./install.sh --from-source
```

Andre agenter, Windows, MCP-serveren og valgene i installasjonsskriptet står i
[docs/install.md](docs/install.md) (på engelsk, som resten av dokumentasjonen).

## Første gang

```bash
export FIKEN_API_TOKEN=...   # personlig API-nøkkel fra Fiken: Innstillinger -> API
fiken-cli doctor             # pålogging, forbindelse og tilstanden til speilet
fiken-cli companies          # finn selskapets slug; alle kommandoer gjelder ett selskap
fiken-cli sync               # bygg speilet; senere kjøringer henter bare det som er endret
```

OAuth2 for å handle på vegne av andre selskaper: `fiken-cli auth login`. Se
[docs/configuration.md](docs/configuration.md). Hvordan synkingen virker, med tidsvinduer og
full ny henting, står i [docs/sync.md](docs/sync.md).

## En MVA-termin fra start til slutt

Dette er rekkefølgen som fungerer, og den skillene følger.

1. **Finn den åpne terminen.** En levert termin etterlater et MVA-oppgjørsbilag i
   hovedboken, en betalt termin et betalingsbilag. Agenten leser dem fra speilet og sier
   hvilke terminer som er levert, betalt, åpne eller forfalt.
2. **Kartlegg banklinjene.** Agenten åpner Superføring for terminens datoer og sorterer
   hver åpne linje: klar til bekreftelse, må bokføres og har bilag, må bokføres og mangler
   bilag, eller trenger din vurdering. Du får den tabellen før noe blir skrevet.
3. **Bokfør og bekreft.** Når du sier fra, bokfører den hver linje som et betalt kjøp i NOK
   med bankens beløp, legger ved bilaget, fjerner kopien i innboksen og bekrefter linjen.
   Ligger ikke kvitteringen i Fiken-innboksen, leter den i e-posten din, etter å ha spurt
   deg hvilken e-posttilgang den skal bruke.
4. **Feilsøk.** `drift`, `vat-anomaly` og `duplicates` for perioden.
5. **Kontroller beløpet.** `mva-summary` viser postene i meldingen. Beløpet du kan stole på
   er hovedboken: summen av MVA-kontoene 2700 til 2713 for perioden. Under «Kjente hull»
   står det hvorfor de to kan avvike.
6. **Lever.** Du åpner meldingen i Fiken, sammenligner og leverer.

## Nettleserdelen

Superføring kjører i en ekte, synlig Chrome der du er logget inn. Agenten kobler seg til
over DevTools-porten og håndterer aldri Fiken-passordet ditt.

- På samme maskin: agenten finner en nettleser som alt kjører med debug-port, eller starter
  en med egen profilmappe, slik at Fiken-påloggingen overlever mellom kjøringer.
- Over ssh, uten skjermdeling: du starter Chrome på din egen maskin og åpner en omvendt
  tunnel. Agenten jobber gjennom den mens du ser på fanen.

Oppsett og kommandoer: [skills/fiken-superforing/surfaces/cdp.md](skills/fiken-superforing/surfaces/cdp.md).

## Det som beskytter regnskapet

- **Testselskap som standard.** All skriving avvises med mindre selskapet er et
  testselskap i Fiken. Ekte regnskap krever `FIKEN_MODE=live` i en env-fil som ikke ligger
  i git. Det finnes ikke noe flagg for det, og MCP-serveren er alltid i testmodus.
- **Lesing før skriving.** Ber du agenten se, liste opp eller undersøke, er det en
  kartlegging: den rapporterer og stopper. Den bokfører og bekrefter bare det du ber om.
- **Banklinjen er fasit.** Et kjøp bokføres med bankens dato og beløp i NOK, og innholdet
  kontrolleres mot linjen før det sendes.
- **Samme faktura bokføres ikke to ganger.** Før et kjøp opprettes, slås leverandørens
  fakturanummer opp i regnskapet. Kvitteringer som alt er bokført kan fortsatt ligge ubrukt
  i innboksen, så innboksen beviser ingenting.
- **Revisjonslogg.** Hvert kjøp, hver betaling, hver sletting i innboksen og hver bekreftet
  linje føres i en lokal logg, med én korrelasjons-ID per kjøring.

## To måter agenten skriver på

- **`commit`, `reconcile`, `reverse`.** De sikrede kommandoene: validert, logget i samme
  kall, og for `commit` også sperret mot gjentakelse, slik at samme kilde aldri bokføres to
  ganger. `prepare` og `validate` bygger og kontrollerer forslaget først.
- **De genererte kommandoene** (`purchases create`, betalinger, vedlegg). De skriver rett
  til Fiken. Superføring-flytene bruker disse, og står selv for kontrollene og loggføringen.

Om Superføring bør flyttes over på `commit` er et åpent designspørsmål. Det står i
[docs/design.md](docs/design.md) sammen med de andre hullene i skrivestien.

## Kjente hull

Sagt rett ut, så ingenting over leses som en garanti det ikke er. Hvert punkt har en issue.

- `mva-summary` trekker ikke fra kreditnotaer. En periode med en kreditert faktura viser
  derfor for høy utgående MVA ([#24](https://github.com/nytafar/fiken-cli/issues/24)).
- `purchases create` har ingen innebygd sperre mot et fakturanummer som alt er bokført;
  skillen gjør oppslaget ([#25](https://github.com/nytafar/fiken-cli/issues/25)). Den må
  også slå opp det nye kjøpet etterpå, fordi kommandoen ikke returnerer noen id
  ([#26](https://github.com/nytafar/fiken-cli/issues/26)).
- `missing-bilag` lister også betalinger, oppgjør og lønnsposter, som ikke trenger eget
  bilag ([#28](https://github.com/nytafar/fiken-cli/issues/28)).
- `bank-unverified` tar utgangspunkt i avstemmingsdatoen Fiken oppgir per konto. Er den
  datoen gammel, blir listen lang og sier lite; de åpne linjene finner du i Superføring.
- Det finnes ennå ingen kommando for terminstatus
  ([#30](https://github.com/nytafar/fiken-cli/issues/30)) eller for faste leverandører som
  mangler en måned ([#31](https://github.com/nytafar/fiken-cli/issues/31)).

## Dokumentasjon

Dokumentasjonen under er på engelsk.

- [docs/features.md](docs/features.md): kontrollene, skrivekommandoene, analysene, rapportformat, perioder og oppskrifter.
- [docs/sync.md](docs/sync.md): speilet, inkrementell synking, tidsvinduer og full ny henting.
- [docs/commands.md](docs/commands.md): hele kommandoreferansen, utdataformater, agentflagg og exit-koder.
- [docs/configuration.md](docs/configuration.md): pålogging, skrivesperren og live-modus, konfigurasjonsfil, feilsøking.
- [docs/install.md](docs/install.md): valg i installasjonsskriptet, andre agenter, bygging fra kilde, utvikling av skillene.
- [docs/mcp.md](docs/mcp.md): `fiken-mcp` for Claude Code og Claude Desktop; [docs/serving-publicly.md](docs/serving-publicly.md) for et offentlig endepunkt.
- [docs/design.md](docs/design.md): designprinsipper og veikart over kjente hull.
- [skills/fiken/SKILL.md](skills/fiken/SKILL.md) og [skills/fiken-superforing/SKILL.md](skills/fiken-superforing/SKILL.md): det agenten leser.
- [CHANGELOG.md](CHANGELOG.md).
