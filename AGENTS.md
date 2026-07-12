# codetospec — guide agent

Binaire Go qui lit un dépôt et produit un graphe markdown de règles métier
(EARS), citées ligne à ligne et vérifiées. Pour le quoi/pourquoi, voir
`README.md`. Ici : comment travailler dans le repo.

## Commandes

```sh
go build -o bin/codetospec ./cmd/codetospec   # TOUJOURS rebuild avant un run réel
make test                                      # go vet ./... && go test ./...
make run-fixture                               # run mocké sur testdata/fixture
```

- **Rebuild `bin/codetospec` après toute modif de code avant de le lancer** —
  un binaire périmé produit un run faux silencieusement (déjà arrivé).
- CGO obligatoire (tree-sitter) : `CGO_ENABLED=1`, toolchain C requise.
- `go test ./...` tourne sans réseau ni PHP. Tests taggés `phplocal` pour
  l'extracteur PHP (skip si `php` absent).

## Architecture

Pipeline : `extract → chunk → map → reduce → crosscheck(+repair) → build → digest → verify → render`.
- `internal/sitter` — tree-sitter, une grammaire + un `.scm` par langage.
  Ajouter un langage = grammaire + fichier query, zéro modif du cœur.
- `internal/extract` — modèle Fact, fusion, protocole extracteurs externes.
- `internal/{mapper,reducer,crosscheck}` — phases LLM, chacune valide
  mécaniquement sa sortie (2 corrections max puis échec tracé). Le crosscheck
  (`--crosscheck`) réfute chaque règle ; avec `--repair` (+ index SCIP), une
  règle rejetée re-cite le span exact d'un symbole précis, adopté seulement
  s'il chevauche un vrai corps de symbole (grounding mécanique).
- `internal/{graph,verify,render}` — assemblage déterministe, contrôles, écriture.
- `internal/drift` — digest du code cité par règle (frontmatter `digest:`) ;
  commande `drift` = re-hache et signale les règles périmées (déterministe).
- `internal/consistency` — détection de règles quasi-doublons (Jaccard sur
  les exigences), listées dans le README de sortie.
- `cmd/codetospec/main.go` — séquence complète.

## Extracteurs = modules Go SÉPARÉS

`extractors/php/` (composer), `extractors/go/` (`go.mod` propre, dépend de
`golang.org/x/tools`) et `extractors/scip/` (dépend de `github.com/scip-code/scip`
+ protobuf) **ne sont pas** construits par `go build ./...` depuis la racine.
Les tester/builder depuis leur dossier. Raison : le module principal a un jeu
de dépendances verrouillé (yaml, tree-sitter, charm) — les extracteurs vivent
hors de ce jeu. Le convertisseur SCIP émet des facts `symbol` avec
`precise: true` que le map injecte comme ancres de citation.

## Cache & reprise (`<out>/.codetospec/`)

- `map/<chunkID>.json` — clé = hash du contenu du chunk.
- `reduce/<domain>.json` — clé = domaine.
- `crosscheck/<ruleID>.json` — clé = hash (règle + lignes citées).

Reprise = existence de ces fichiers. Pour rejouer une phase, **purger son
cache** (`rm -rf .codetospec/reduce`) puis relancer ; les phases amont
restantes sont réutilisées. `state.json` cumule les tokens entre runs.

Gotcha : map (clé = hash contenu) et crosscheck (clé = hash règle+lignes)
sont *content-aware* — ils se refont si le code change. Le **reduce** est
indexé par nom de domaine seul : après une édition du code source, purger
`.codetospec/reduce` à la main, sinon les sorties reduce restent périmées.

## Conventions

- Sortie déterministe : tris stables partout, deux runs cache chaud =
  identiques octet pour octet. Ne jamais introduire d'ordre non déterministe.
- Code/commentaires/identifiants en **anglais** ; comms utilisateur en français.
- Golden tests render : `go test ./internal/render -update` pour régénérer.
- README tenu à jour à chaque feature livrée (concis).

## Notes

Le reduce découpe automatiquement les domaines de plus de `--reduce-batch`
candidates (défaut 30) en lots fusionnés déterministiquement ; un lot qui
tronque encore est re-découpé en deux et réessayé (halving rapide, sans
corrections gaspillées) jusqu'à un plancher. Un gros domaine ne perd plus ses
règles. La fusion inter-lots déduplique par requirement exact et renumérote
les slugs en collision (pas de dédup sémantique fine entre lots : limite
assumée). Attention : un domaine à plusieurs centaines de candidates (ex. des
resolvers GraphQL) reste lent — souvent de la plomberie à exclure en amont.
