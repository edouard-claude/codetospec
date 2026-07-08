# codetospec v0.1 : spécification d'implémentation

Spécification exécutable. Implémenter exactement ce document, d'une seule traite, sans poser de question. Toute décision est déjà prise ici. Cette version remplace la v0.

## 1. Objectif

Binaire Go unique, `codetospec`, **langage-agnostique**, qui lit un dépôt de code source, en extrait les règles métier et produit un knowledge graph en markdown versionnable : des nœuds `.md` (domaines, entités, endpoints, règles au format EARS) reliés par des edges typés en frontmatter YAML et par des liens markdown inline. La sortie est consommable par un humain (GitLab, Obsidian) et par des agents (graph.json, llms.txt).

Le cœur du programme ne contient aucune connaissance d'un langage particulier. La connaissance des langages vit dans trois couches :

1. **tree-sitter** (structure universelle) : AST error-tolerant pour le chunking et les symboles, quel que soit l'état du code.
2. **Extracteurs natifs externes** (sémantique d'écosystème) : exécutables sous contrat JSON, utilisant les outils officiels du langage cible. Un extracteur PHP est livré comme premier cas d'usage.
3. **Le LLM** : lit les chunks directement, nativement multi-langage.

Le programme tient la boucle ; le LLM fait le travail cognitif dans des budgets et des vérificateurs déterministes. Toute sortie LLM est validée mécaniquement (schéma JSON, citations résolvables, références existantes) avant d'entrer dans le graphe. Les facts extraits par les couches 1 et 2 sont les ancres de vérification : le LLM les consomme, ne les produit jamais.

### Non-buts v0.1

Pas de reduce agentique avec tool calls, pas de SQLite, pas de serveur MCP, pas de connecteurs Jira/Confluence, pas de drift CI, pas de clustering LLM des domaines. Roadmap v1, hors scope.

## 2. Contraintes techniques

- Go >= 1.26. Module : `codetospec`.
- CGo **autorisé uniquement** pour tree-sitter. Build : `go build ./...` (CGO_ENABLED=1).
- Dépendances Go autorisées, aucune autre :
  - `gopkg.in/yaml.v3`
  - `github.com/tree-sitter/go-tree-sitter` (bindings officiels)
  - Grammaires officielles : `tree-sitter-php`, `tree-sitter-go`, `tree-sitter-javascript`, `tree-sitter-typescript`, `tree-sitter-rust`
- `context.Context` propagé partout. SIGINT/SIGTERM : sauvegarde de l'état puis sortie propre (code 130).
- Logs : `log/slog`, handler texte sur stderr, niveau via `--log-level` (défaut `info`).
- Code idiomatique : pas de shadowing, erreurs enveloppées `fmt.Errorf("...: %w", err)`, `any` au lieu de `interface{}`, commentaires et identifiants en anglais.
- Sortie déterministe : tris stables partout ; deux runs sur le même input avec cache chaud produisent des fichiers identiques octet pour octet.
- Concurrence : worker pool maison (channels + `sync.WaitGroup`), taille via `--workers` (défaut 4).

## 3. Arborescence du projet

```
codetospec/
├── go.mod
├── Makefile
├── README.md
├── codetospec.example.yaml
├── cmd/codetospec/main.go        # CLI: run, verify, stats
├── internal/config/config.go     # flags + env + yaml config file
├── internal/source/walker.go     # repo walk, language detection by extension
├── internal/sitter/              # tree-sitter integration (language-neutral API)
│   ├── sitter.go                 # parser registry, one entry per grammar
│   ├── queries/                  # one .scm file per language: defs, imports
│   └── chunk.go                  # AST-based chunking
├── internal/extract/
│   ├── extract.go                # Fact model, merge, dedup
│   ├── universal.go              # facts from tree-sitter symbols
│   └── external.go               # external extractor protocol (exec + JSON)
├── internal/llm/client.go        # OpenAI-compatible HTTP client
├── internal/mapper/mapper.go     # MAP phase
├── internal/reducer/reducer.go   # REDUCE phase
├── internal/graph/graph.go       # node model, build, edge validation
├── internal/verify/verify.go     # citations, references, coverage
├── internal/render/render.go     # markdown nodes, README, llms.txt, graph.json
├── internal/state/state.go       # JSON state store, resume, token costs
├── extractors/php/               # shipped native extractor, first ecosystem
│   ├── composer.json             # nikic/php-parser ^5
│   ├── extract.php               # AST + optional runtime introspection
│   └── README.md
└── testdata/fixture/             # mini PHP project (section 13)
```

## 4. CLI et configuration

```
codetospec run --src <repo-dir> --out <graph-dir> [flags]
codetospec verify --src <repo-dir> --out <graph-dir>
codetospec stats --out <graph-dir>
```

Flags de `run` :

| Flag | Défaut | Rôle |
|---|---|---|
| `--src` | requis | racine du dépôt analysé |
| `--out` | requis | dossier du graphe de sortie |
| `--config` | `<src>/codetospec.yaml` si présent | fichier de config |
| `--base-url` | env `LLM_BASE_URL` | endpoint OpenAI-compatible |
| `--api-key` | env `LLM_API_KEY` | vide accepté (vLLM local) |
| `--model` | env `LLM_MODEL` | nom du modèle |
| `--lang` | `fr` | langue des exigences (`fr` ou `en`) |
| `--workers` | 4 | parallélisme du map |
| `--max-tokens` | 4096 | plafond de génération par appel |
| `--exclude` | `vendor,node_modules,storage,dist,build,.git` | dossiers ignorés |
| `--facts` | vide, répétable | fichiers de facts JSON additionnels |
| `--log-level` | `info` | debug, info, warn, error |

`codetospec.yaml` (tout est optionnel) :

```yaml
exclude: [vendor, node_modules, storage]
extractors:
  - name: php
    cmd: php
    args: [extractors/php/extract.php, --root, "{src}"]
    timeout: 300s
facts_files: []          # same as --facts
domain_strategy: auto    # auto | namespace | directory
```

`run` est idempotent : reprise automatique via le cache d'état. `verify` recontrôle un graphe existant contre les sources, code 1 si violation. `stats` affiche tokens et compteurs par phase.

## 5. Pipeline

```
extract (couches 1+2, Go+exec) -> chunk (tree-sitter) -> map (LLM, parallèle)
  -> reduce (LLM, par domaine) -> build + verify + render (Go pur)
```

### 5.1 EXTRACT : faits déterministes, trois sources fusionnées

Modèle commun :

```go
// Fact is a mechanically provable statement about the codebase.
type Fact struct {
	Kind      string            `json:"kind"`      // "symbol", "route", "table", "module"
	ID        string            `json:"id"`        // stable identifier
	Attrs     map[string]string `json:"attrs"`
	Source    Ref               `json:"source"`
	Origin    string            `json:"origin"`    // "sitter" | extractor name | facts file
	Certainty string            `json:"certainty"` // "proved" | "static"
}

// Ref pins a fact to a file location.
type Ref struct {
	Path  string `json:"path"`  // relative to --src, slash-separated
	Lines string `json:"lines"` // "12-87", 1-based inclusive
}
```

**Source A, universelle (tree-sitter)** : pour chaque fichier d'un langage supporté, exécuter les requêtes `.scm` du langage : définitions (classes, fonctions, méthodes, avec lignes exactes) et imports. Émettre des facts `symbol` (attrs : `name`, `container`, `visibility` si disponible, `language`, `namespace` si le langage en a) et `module` (un par unité de namespace/package rencontrée). Certainty `static`. Les fichiers illisibles ou de langage inconnu ne produisent pas de facts mais restent chunkés (5.2).

**Source B, extracteurs natifs (protocole externe)** : chaque extracteur configuré est exécuté une fois : `cmd args...` avec `{src}` substitué, répertoire de travail = racine de codetospec, timeout configuré. Contrat : stdout = `{"schema": "codetospec/facts/v1", "facts": [Fact...]}` ; stderr = logs libres ; exit non nul ou JSON invalide = warning `extractor failed`, le run continue (dégradation gracieuse tracée). Les facts d'extracteurs portent `Origin` = nom de l'extracteur.

**Source C, fichiers de facts** (`--facts`, `facts_files`) : même schéma JSON, chargés tels quels. Permet d'injecter des facts produits ailleurs (CI, SCIP converti, main humaine) et sert aux tests.

**Fusion** : concaténation, déduplication par `ID` avec priorité `proved` > extracteur > `sitter` (un fact runtime écrase un fact statique de même ID). Résultat écrit dans `<out>/.codetospec/facts.json`.

**Domaines** : `domain_strategy: auto` = namespace/package si les facts en fournissent (attr `namespace` des symbols, règle : segment après le préfixe racine commun, en minuscules), sinon premier dossier sous `--src`. Fonction pure `DomainOf(f Fact, path string) string`, testée. Ids : `domain.<slug>`.

### 5.2 CHUNK : découpage AST

Par fichier, via tree-sitter :

- Un chunk par définition de niveau supérieur (classe, fonction libre) si <= 300 lignes.
- Classe > 300 lignes : un chunk par méthode, préfixé d'un en-tête de contexte (lignes réelles de la déclaration de classe et de ses propriétés).
- Fichier sans définitions (procédural pur) ou langage sans grammaire : chunks de 250 lignes, chevauchement 20.
- Chunk : `{ID (sha256 hex, 16 chars, du contenu), Path, StartLine, EndLine, Language, Namespace, Domain, Content}`.

### 5.3 MAP : règles candidates par chunk

Un appel LLM par chunk, worker pool. Sortie : `<out>/.codetospec/map/<chunkID>.json`, skip si présent (reprise).

Contexte injecté : facts `route` dont l'attr `controller` ou `handler` référence un symbole du fichier, liste complète des ids `entity.*`, domaine, langage.

**System prompt map** (littéral, `<LANG>` substitué) :

```
You are a senior software archaeologist. You read legacy source code in any language and extract candidate business rules.

Hard rules:
- Output ONLY a valid JSON object matching the schema provided by the user. No markdown fences, no prose.
- Every rule MUST cite one or more exact line ranges inside the provided range. Never cite outside it.
- entities MUST be a subset of ALLOWED_ENTITIES. endpoints MUST be a subset of ALLOWED_ENDPOINTS. When unsure, use [].
- Write the requirement in <LANG> using one EARS pattern:
  ubiquitous: "Le systeme doit <response>."
  event: "QUAND <trigger>, le systeme doit <response>."
  state: "TANT QUE <state>, le systeme doit <response>."
  unwanted: "SI <condition>, ALORS le systeme doit <response>."
  optional: "LA OU <feature>, le systeme doit <response>."
- Focus on business behavior: validation, calculation, state transitions, side effects, authorization. Ignore framework plumbing.
- If the chunk contains no business rule, return {"chunk_summary": "...", "rules": []}.
```

**User prompt map** :

```
FILE: <path> (lines <start>-<end>)
LANGUAGE: <language>
NAMESPACE: <namespace or "-">
DOMAIN: <domain>
ALLOWED_ENTITIES: <json array>
ALLOWED_ENDPOINTS: <json array relevant to this file>

OUTPUT JSON SCHEMA:
{"chunk_summary": string, "rules": [{"title": string, "ears_kind": "ubiquitous|event|state|unwanted|optional", "requirement": string, "citations": [{"path": string, "lines": "A-B"}], "entities": [string], "endpoints": [string], "confidence": number}]}

CODE:
<content>
```

**Validation déterministe** (dans `mapper`) : JSON parse ; `ears_kind` dans l'énumération ; chaque citation avec `path` égal au path du chunk et bornes incluses dans `[start,end]` ; `entities`/`endpoints` sous-ensembles autorisés ; `confidence` dans [0,1]. Échec : un message user `output rejected: <erreur précise>; resend the full corrected JSON only`, deux corrections max, sinon chunk `failed`, le run continue.

### 5.4 REDUCE : consolidation par domaine

Groupement par `Domain`, un appel LLM par domaine, séquentiel. Sortie : `<out>/.codetospec/reduce/<domain>.json`, skip si présent.

**System prompt reduce** :

```
You are a requirements engineer. You consolidate candidate business rules extracted from legacy code into a clean, deduplicated specification for one domain.

Hard rules:
- Output ONLY a valid JSON object matching the schema. No markdown fences, no prose.
- Merge duplicates and near-duplicates. Keep the union of their citations verbatim; never invent or alter citations.
- entities and endpoints MUST be subsets of the provided allowed lists.
- Keep requirements in <LANG>, EARS patterns, one behavior per rule.
- slug: lowercase ascii, words separated by "-", max 5 words, unique within the domain.
- acceptance_criteria: 2 to 5 concrete, testable checks per rule, derived from the requirement, written in <LANG>.
```

**User prompt reduce** : `DOMAIN`, `ALLOWED_ENTITIES`, `ALLOWED_ENDPOINTS`, schéma de sortie, puis `CANDIDATE_RULES:` (JSON compact des candidates).

Schéma de sortie :

```
{"domain_summary": string, "rules": [{"slug": string, "title": string, "ears_kind": string, "requirement": string, "rationale": string, "citations": [{"path": string, "lines": "A-B"}], "entities": [string], "endpoints": [string], "acceptance_criteria": [string]}]}
```

Validation : mêmes contrôles que map, plus unicité des slugs, plus **chaque citation doit exister mot pour mot (path+lines exacts) dans l'union des citations candidates du domaine**. Deux corrections max, sinon domaine `failed`, le run continue.

### 5.5 BUILD + VERIFY + RENDER

`graph.Build` assemble, `verify` contrôle, `render` écrit. Aucune écriture dans `nodes/` avant que verify passe.

## 6. Modèle de graphe

```go
// Node is one markdown file in the spec graph.
type Node struct {
	ID      string            // "rule.billing.prorata-activation"
	Type    string            // "domain" | "entity" | "endpoint" | "rule"
	Status  string            // always "generated" in v0.1
	Sources []Ref
	Edges   []Edge
	Title   string
	Body    string            // markdown body without frontmatter
	Extra   map[string]string // ears, acceptance (rules only)
}

// Edge links two nodes.
type Edge struct {
	Type string // "belongs_to" | "touches" | "exposed_by" | "depends_on"
	To   string
}
```

- `domain.<slug>` : un par domaine ayant au moins une règle ; body = domain_summary + liste liée des règles ; edges `depends_on` calculés déterministiquement depuis les facts d'imports croisant les domaines, jamais par le LLM.
- `entity.<table>` : depuis les facts `table` ; body = colonnes ; source = le fact.
- `endpoint.<verb>-<slug>` : depuis les facts `route` ; body = verbe, path, handler.
- `rule.<domain>.<slug>` : depuis le reduce ; edges `belongs_to`, `touches`, `exposed_by` ; sources = citations ; Extra `ears`, `acceptance`.

## 7. Vérifications (verify)

Échec de build (code 1, rien n'est rendu) si :

1. ID dupliqué.
2. Edge vers un ID inexistant.
3. Citation non résolvable : path absent de `--src` ou bornes hors du fichier réel.
4. Nœud `rule` sans citation.
5. Frontmatter régénéré non re-parsable (auto-test de round-trip).

Rapport de couverture (warnings) dans le résumé et `README.md` : % endpoints référencés par >= 1 règle, % entités touchées, chunks/domaines `failed`, fichiers sans grammaire (fallback lignes), extracteurs en échec.

## 8. Rendu de sortie

```
<out>/
├── llms.txt
├── README.md
├── graph.json
└── nodes/
    ├── domains/<slug>.md
    ├── entities/<slug>.md
    ├── endpoints/<slug>.md
    └── rules/<domain>.<slug>.md
```

Frontmatter exact d'un nœud rule :

```markdown
---
id: rule.billing.prorata-activation
type: rule
status: generated
sources:
  - path: app/Services/Billing/ProrataCalculator.php
    lines: "42-118"
edges:
  - {type: belongs_to, to: domain.billing}
  - {type: touches, to: entity.invoices}
  - {type: exposed_by, to: endpoint.post-api-activate}
ears: event
acceptance: 3
---

# Prorata à l'activation

**Exigence (EARS)** : QUAND un abonne active en cours de mois, le systeme doit facturer au prorata des jours restants.

**Justification** : <rationale>

**Critères d'acceptation** :
1. ...

**Sources** : `app/Services/Billing/ProrataCalculator.php:42-118`

Liens : [Domaine billing](../domains/billing.md) · [entity.invoices](../entities/invoices.md) · [endpoint](../endpoints/post-api-activate.md)
```

Liens inline générés pour chaque edge, chemins relatifs corrects. `graph.json` : `{"nodes": {"<id>": {"type", "file", "edges"}}}`, clés triées. `llms.txt` : description, navigation (commencer par `nodes/domains/`, suivre les edges), inventaire par type. `README.md` : synthèse, couverture, Mermaid `graph TD` des domaines et `depends_on`.

## 9. Extracteur PHP livré (`extractors/php/`)

Premier extracteur natif, preuve du protocole. Script PHP CLI, dépendance `nikic/php-parser ^5` (le parser AST de l'écosystème, socle de PHPStan et Rector).

Comportement de `extract.php --root <src>` :

1. **AST statique** (toujours) : parcourir les `.php` hors exclusions ; namespaces, classes (extends, implements, méthodes publiques, lignes exactes depuis l'AST), appels `Route::<verb>(...)` (facts `route`, certainty `static`), blocs `Schema::create('<table>', ...)` avec colonnes `$table-><type>('<name>')` (facts `table`).
2. **Introspection runtime** (si `--boot` et si `artisan` présent et exécutable) : `php artisan route:list --json` ; les routes obtenues remplacent les routes statiques (certainty `proved`). Échec du boot : warning stderr, on garde le statique.
3. Sortie stdout : `{"schema": "codetospec/facts/v1", "facts": [...]}`.

Le binaire Go ignore tout de ce script : il ne connaît que le protocole. `extractors/php/README.md` documente `composer install` et l'entrée de config correspondante.

## 10. Client LLM

`internal/llm` : identique à un client OpenAI-compatible minimal.

```go
// Client talks to any OpenAI-compatible chat completions endpoint.
type Client struct { /* baseURL, apiKey, model, maxTokens, httpClient */ }

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (c *Client) Chat(ctx context.Context, msgs []Message) (string, Usage, error)
```

POST `<base>/chat/completions`, body `{"model", "messages", "temperature": 0.1, "max_tokens": <config>}`, `Authorization: Bearer <key>` si key non vide. Timeout 180s. Retry réseau/5xx/429 : 3 tentatives, backoff 2s, 8s, 20s. Avant unmarshal : trim + retrait des fences ``` éventuelles.

## 11. État, reprise, coûts

`<out>/.codetospec/state.json`, écriture atomique (temp + rename) après chaque unité terminée et sur signal :

```json
{
  "version": 1,
  "src_path": "...",
  "started_at": "RFC3339",
  "phase": "map",
  "chunks_done": 412, "chunks_failed": 3, "chunks_total": 947,
  "domains_done": 2, "domains_failed": 0, "domains_total": 9,
  "extractors": {"php": "ok"},
  "tokens": {"map": {"prompt": 0, "completion": 0}, "reduce": {"prompt": 0, "completion": 0}}
}
```

La reprise réelle est portée par l'existence des fichiers `map/<chunkID>.json` et `reduce/<domain>.json`. `chunkID` dépend du contenu : un fichier modifié est re-mappé naturellement.

## 12. main.go : séquence

1. Parse config (flags > yaml > env), valide.
2. Walk + détection de langage ; extract A (sitter), B (extracteurs), C (facts files) ; fusion ; `facts.json`.
3. Chunk (tree-sitter).
4. Map (worker pool), progression loggée toutes les 25 unités.
5. Reduce séquentiel, domaines triés.
6. Build, verify ; si OK, render (vide puis réécrit `nodes/`, `graph.json`, `README.md`, `llms.txt` ; ne touche jamais `.codetospec/`).
7. Résumé : nœuds par type, couverture, tokens, échecs, extracteurs.

## 13. Fixture de test (`testdata/fixture/`)

Mini projet PHP réel, parsé par tree-sitter dans les tests (pas besoin de PHP installé) :

- `routes/web.php` : `Route::post('/api/activate', [App\Http\Controllers\ActivationController::class, 'store']);` + une route GET.
- `database/migrations/2020_01_01_000000_create_invoices_table.php` : `Schema::create('invoices', ...)`, 4 colonnes.
- `app/Models/Invoice.php`.
- `app/Services/Billing/ProrataCalculator.php` : namespace `App\Services\Billing`, méthode `calculate` avec une vraie règle (prorata si activation en cours de mois, exception si montant négatif).
- `app/Http/Controllers/ActivationController.php`.
- `fixture.facts.json` : facts `route` et `table` de la fixture, au schéma du protocole, pour le end-to-end via `--facts` (simule un extracteur natif sans dépendre de PHP).

## 14. Tests

`go test ./...`, sans réseau, sans PHP installé :

- `sitter` : parsing de la fixture, définitions et bornes exactes, namespaces PHP extraits, fallback lignes sur un fichier `.xyz` inconnu.
- `extract` : fusion des trois sources, priorité `proved` > extracteur > `sitter`, DomainOf.
- `external` : extracteur mock = `go run ./internal/extract/testdata/echoextractor` qui imprime un facts JSON ; test du succès, du timeout, de l'exit non nul (warning, run continue).
- `chunk` : bornes, en-tête de contexte des chunks de méthode, chevauchement du fallback.
- `mapper`/`reducer` : LLM mocké (interface locale implémentée par une func) ; nominal, rejet + correction, double échec.
- `graph` + `verify` : edge orphelin rejeté ; citation hors bornes rejetée ; round-trip frontmatter.
- `render` : golden test, liens relatifs valides.
- End-to-end : `run` complet sur la fixture avec mock LLM + `--facts fixture.facts.json` ; graphe non vide, verify passe.
- Optionnel, taggé `//go:build phplocal` : test de `extractors/php/extract.php` si `php` et composer sont disponibles, skip sinon.

## 15. Makefile

```make
build:
	go build -o bin/codetospec ./cmd/codetospec
test:
	go vet ./... && go test ./...
run-fixture:
	go run ./cmd/codetospec run --src testdata/fixture --out /tmp/spec-graph-fixture --facts testdata/fixture/fixture.facts.json
```

## 16. README.md du projet

Court : ce que fait l'outil, l'architecture trois couches (une phrase chacune), prérequis (endpoint OpenAI-compatible : vLLM auto-hébergé ou API ; toolchain C pour CGo), variables d'env, exemple de run avec et sans extracteur PHP, structure de la sortie, protocole extracteur (schéma facts v1), limites v0.1.

## 17. Definition of Done

1. `go build ./...` et `go vet ./...` sans erreur.
2. `go test ./...` vert, incluant le end-to-end mocké, sans PHP ni réseau.
3. `codetospec run` sur la fixture avec un vrai endpoint LLM produit : >= 1 domaine, >= 1 entité, >= 1 endpoint, >= 1 règle EARS citée ; `verify` passe ; `README.md` de sortie contient Mermaid et couverture.
4. Le même run avec en plus l'extracteur PHP configuré (machine disposant de php+composer) produit des routes `proved` qui écrasent les routes `static`.
5. Ctrl-C pendant le map : état sauvé ; relance : chunks déjà faits sautés.
6. Deux runs complets consécutifs (cache chaud) : sortie identique octet pour octet.
7. Aucune écriture hors de `--out`.
8. Ajouter un langage à la couche universelle = ajouter une grammaire et un fichier `.scm`, zéro modification du cœur ; ajouter un écosystème = écrire un extracteur externe, zéro modification du binaire.

## 18. Roadmap v1, hors scope, ne pas implémenter

Reduce agentique avec tools en lecture seule (get_source, get_facts, graph_neighbors) et budgets ; ingestion SCIP ; SQLite ; connecteurs Jira/Confluence ; delta doc AMOA vs code ; serveur MCP sur graph.db ; drift-check CI ; clustering LLM des bounded contexts ; extracteurs supplémentaires (Go natif via go/packages, TypeScript via compiler API).
