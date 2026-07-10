# Extracteur Go natif

Extracteur natif codetospec pour l'écosystème Go, sur le protocole
`codetospec/facts/v1`. Il émet ce que tree-sitter ne peut pas voir : les
**routes** HTTP et les **tables** SQL.

## Ce qu'il extrait

- **Routes** (`route`) via `golang.org/x/tools/go/packages` (types résolus) :
  gin/echo (`r.GET("/x", h)`), chi (`r.Get(...)`), net/http
  (`mux.HandleFunc("GET /x", h)`), plus `Handle(method, path, …)` et
  `Any`. Les préfixes de groupe (`api := r.Group("/api")`) sont résolus, et
  le handler est nommé via l'information de type (`pkg.Func`,
  `Type.Méthode`).
- **Tables** (`table`) : `CREATE TABLE` des fichiers `.sql` (schéma sqlc,
  migrations), avec colonnes typées ; les contraintes de table sont ignorées.

## Prérequis

Le module cible doit être chargeable par `go/packages` (toolchain Go
présente, dépendances résolvables). Les erreurs de compilation partielles
sont tolérées : l'extraction continue avec l'information de type disponible.

## Utilisation

```sh
cd extractors/go && go build -o extract .
```

Entrée `codetospec.yaml` du dépôt analysé :

```yaml
extractors:
  - name: go
    cmd: go
    args: [run, ./extractors/go, --root, "{src}"]
    timeout: 300s
```

Option `--sql-glob "sql/schema/*.sql"` pour restreindre les fichiers de
schéma scannés.
