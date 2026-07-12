# Convertisseur SCIP

Transforme un index [SCIP](https://github.com/sourcegraph/scip) (protocole de
code intelligence) en facts codetospec (`codetospec/facts/v1`). Il émet des
facts `symbol` avec les **bornes exactes** qu'un indexeur a résolues — bien
plus précises que tree-sitter. C'est tout l'intérêt : des localisations de
symboles exactes permettent au map de citer les bonnes lignes au lieu de
deviner (lignes vides, déclarations de types sans logique).

Les facts portent `certainty: proved` et un attribut `precise: true` ; le
binaire injecte ces définitions (nom, bornes, signature) dans le contexte du
chunk pendant le map.

## Prérequis

Un index SCIP `index.scip`, produit par un indexeur de langage — jamais par
cet outil :

```sh
# Go
go install github.com/scip-code/scip-go/cmd/scip-go@latest
scip-go --output index.scip ./...

# TypeScript : scip-typescript · C/C++ : scip-clang · etc.
```

## Utilisation

```sh
cd extractors/scip && go build -o scip2facts .
./scip2facts --index index.scip --root . > scip.facts.json
codetospec run --src . --out ./spec --facts scip.facts.json
```

`--root` retire un préfixe des chemins de documents. Les chemins hors dépôt
(stdlib, dépendances, artefacts de build) sont ignorés.

Module Go séparé (dépend de `github.com/scip-code/scip` et de protobuf), hors
du jeu de dépendances du binaire principal.
