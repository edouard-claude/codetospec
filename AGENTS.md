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
  mécaniquement sa sortie (2 corrections max puis échec tracé). Le map préfixe
  chaque ligne du code de son numéro absolu (`numberedContent`) : sinon le
  modèle *compte* les lignes et dérive (citations off-by-N sur gros chunks). Le crosscheck
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
- `reduce/<domain>.json` — réutilisé si le hash des candidates (`candidates_hash`) correspond. Ce hash **fold une version de prompt** (`promptVersion`) : bumper cette constante à chaque édition du prompt reduce, sinon un changement de prompt est silencieusement ignoré sur cache chaud (le hash ne dépend que du contenu des candidates).
- `crosscheck/<ruleID>.json` — clé = hash (règle + lignes citées).

Reprise = existence de ces fichiers. Pour rejouer une phase, **purger son
cache** (`rm -rf .codetospec/reduce`) puis relancer ; les phases amont
restantes sont réutilisées. `state.json` cumule les tokens entre runs.

Les trois phases sont *content-aware* : map (clé = hash contenu du chunk),
reduce (clé = hash des candidates du domaine, stocké dans le `.json`) et
crosscheck (clé = hash règle+lignes). Après une édition du code, le re-run
refait automatiquement ce qui a changé — plus de purge manuelle du reduce.
Le reduce tourne aussi en worker pool (`--workers`), les domaines étant
indépendants (le `depends_on` est calculé au build).

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
