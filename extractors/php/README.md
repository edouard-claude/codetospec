# Extracteur PHP natif

Premier extracteur natif de codetospec, preuve du protocole `codetospec/facts/v1`.
Il utilise [nikic/php-parser](https://github.com/nikic/PHP-Parser) v5, le parser
AST de l'écosystème PHP (socle de PHPStan et Rector).

## Installation

```sh
cd extractors/php
composer install
```

## Ce qu'il extrait

1. **AST statique** (toujours) : namespaces, classes (extends, implements,
   méthodes publiques, lignes exactes), appels `Route::<verb>(...)` (facts
   `route`, certainty `static`), blocs `Schema::create('<table>', ...)` avec
   colonnes `$table-><type>('<name>')` (facts `table`).
2. **Introspection runtime** (option `--boot`, si un `artisan` exécutable est
   présent) : `php artisan route:list --json` ; les routes obtenues remplacent
   les routes statiques (certainty `proved`). En cas d'échec du boot, warning
   sur stderr et conservation du statique.

Sortie stdout : `{"schema": "codetospec/facts/v1", "facts": [...]}`.

## Configuration codetospec

Dans `codetospec.yaml` du dépôt analysé :

```yaml
extractors:
  - name: php
    cmd: php
    args: [extractors/php/extract.php, --root, "{src}"]
    timeout: 300s
```

Ajouter `--boot` aux args pour activer l'introspection runtime.
Le binaire Go ignore tout de ce script : il ne connaît que le protocole.
