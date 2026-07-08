#!/usr/bin/env php
<?php

/**
 * codetospec native PHP extractor.
 *
 * Emits facts JSON (schema codetospec/facts/v1) on stdout:
 *   - module facts for namespaces
 *   - symbol facts for classes and their public methods (exact AST lines)
 *   - route facts for Route::<verb>(...) calls (static)
 *   - table facts for Schema::create('<table>', ...) blocks with columns
 *
 * With --boot, and when an executable artisan file exists under --root,
 * routes from `php artisan route:list --json` replace the static ones
 * (certainty "proved"). Boot failures fall back to static routes.
 *
 * Usage: php extract.php --root <src> [--boot]
 */

declare(strict_types=1);

error_reporting(E_ALL);

$autoloads = [
    __DIR__ . '/vendor/autoload.php',
    __DIR__ . '/../../vendor/autoload.php',
];
$loaded = false;
foreach ($autoloads as $autoload) {
    if (file_exists($autoload)) {
        require $autoload;
        $loaded = true;
        break;
    }
}
if (!$loaded) {
    fwrite(STDERR, "extract.php: vendor/autoload.php not found, run `composer install` in extractors/php\n");
    exit(1);
}

use PhpParser\Node;
use PhpParser\NodeFinder;
use PhpParser\NodeTraverser;
use PhpParser\NodeVisitor\NameResolver;
use PhpParser\ParserFactory;

const EXCLUDED_DIRS = ['vendor', 'node_modules', 'storage', 'dist', 'build', '.git'];

[$root, $boot] = parseArgs($argv);
if ($root === null || !is_dir($root)) {
    fwrite(STDERR, "extract.php: usage: php extract.php --root <src> [--boot]\n");
    exit(1);
}
$root = rtrim($root, '/');

$parser = (new ParserFactory())->createForNewestSupportedVersion();
$finder = new NodeFinder();
$facts = [];

foreach (phpFiles($root) as $absPath) {
    $relPath = substr($absPath, strlen($root) + 1);
    $code = file_get_contents($absPath);
    if ($code === false) {
        fwrite(STDERR, "extract.php: unreadable file $relPath\n");
        continue;
    }
    try {
        $stmts = $parser->parse($code);
    } catch (Throwable $e) {
        fwrite(STDERR, "extract.php: parse error in $relPath: {$e->getMessage()}\n");
        continue;
    }
    if ($stmts === null) {
        continue;
    }
    $traverser = new NodeTraverser();
    $traverser->addVisitor(new NameResolver());
    $stmts = $traverser->traverse($stmts);

    collectNamespaces($finder, $stmts, $relPath, $facts);
    collectClasses($finder, $stmts, $relPath, $facts);
    collectRoutes($finder, $stmts, $relPath, $facts);
    collectTables($finder, $stmts, $relPath, $facts);
}

if ($boot) {
    $facts = replaceRoutesWithRuntime($root, $facts);
}

echo json_encode(
    ['schema' => 'codetospec/facts/v1', 'facts' => $facts],
    JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE
), "\n";
exit(0);

function parseArgs(array $argv): array
{
    $root = null;
    $boot = false;
    for ($i = 1; $i < count($argv); $i++) {
        if ($argv[$i] === '--root' && isset($argv[$i + 1])) {
            $root = $argv[++$i];
        } elseif (str_starts_with($argv[$i], '--root=')) {
            $root = substr($argv[$i], strlen('--root='));
        } elseif ($argv[$i] === '--boot') {
            $boot = true;
        }
    }
    return [$root, $boot];
}

/** @return iterable<string> */
function phpFiles(string $root): iterable
{
    $dirIterator = new RecursiveDirectoryIterator($root, FilesystemIterator::SKIP_DOTS);
    $filter = new RecursiveCallbackFilterIterator($dirIterator, function ($current) {
        if ($current->isDir()) {
            return !in_array($current->getFilename(), EXCLUDED_DIRS, true)
                && !str_starts_with($current->getFilename(), '.');
        }
        return str_ends_with($current->getFilename(), '.php');
    });
    $paths = [];
    foreach (new RecursiveIteratorIterator($filter) as $file) {
        $paths[] = $file->getPathname();
    }
    sort($paths);
    return $paths;
}

function refFor(Node $node, string $relPath): array
{
    return ['path' => $relPath, 'lines' => $node->getStartLine() . '-' . $node->getEndLine()];
}

function collectNamespaces(NodeFinder $finder, array $stmts, string $relPath, array &$facts): void
{
    foreach ($finder->findInstanceOf($stmts, Node\Stmt\Namespace_::class) as $ns) {
        if ($ns->name === null) {
            continue;
        }
        $name = $ns->name->toString();
        $facts[] = [
            'kind' => 'module',
            'id' => "module.$name",
            'attrs' => ['name' => $name, 'language' => 'php'],
            'source' => refFor($ns, $relPath),
            'origin' => 'php',
            'certainty' => 'static',
        ];
    }
}

function collectClasses(NodeFinder $finder, array $stmts, string $relPath, array &$facts): void
{
    foreach ($finder->findInstanceOf($stmts, Node\Stmt\Class_::class) as $class) {
        if ($class->name === null) {
            continue; // anonymous class
        }
        $name = $class->name->toString();
        $namespace = $class->namespacedName !== null
            ? implode('\\', array_slice($class->namespacedName->getParts(), 0, -1))
            : '';
        $attrs = ['name' => $name, 'kind' => 'class', 'language' => 'php'];
        if ($namespace !== '') {
            $attrs['namespace'] = $namespace;
        }
        if ($class->extends !== null) {
            $attrs['extends'] = $class->extends->toString();
        }
        if ($class->implements !== []) {
            $attrs['implements'] = implode(',', array_map(
                static fn ($i) => $i->toString(),
                $class->implements
            ));
        }
        $facts[] = [
            'kind' => 'symbol',
            'id' => "symbol.$relPath#$name:{$class->getStartLine()}",
            'attrs' => $attrs,
            'source' => refFor($class, $relPath),
            'origin' => 'php',
            'certainty' => 'static',
        ];
        foreach ($class->getMethods() as $method) {
            if (!$method->isPublic()) {
                continue;
            }
            $methodName = $method->name->toString();
            $methodAttrs = [
                'name' => $methodName,
                'kind' => 'method',
                'container' => $name,
                'visibility' => 'public',
                'language' => 'php',
            ];
            if ($namespace !== '') {
                $methodAttrs['namespace'] = $namespace;
            }
            $facts[] = [
                'kind' => 'symbol',
                'id' => "symbol.$relPath#$methodName:{$method->getStartLine()}",
                'attrs' => $methodAttrs,
                'source' => refFor($method, $relPath),
                'origin' => 'php',
                'certainty' => 'static',
            ];
        }
    }
}

function collectRoutes(NodeFinder $finder, array $stmts, string $relPath, array &$facts): void
{
    $verbs = ['get', 'post', 'put', 'patch', 'delete', 'options', 'any'];
    foreach ($finder->findInstanceOf($stmts, Node\Expr\StaticCall::class) as $call) {
        if (!$call->class instanceof Node\Name || !$call->name instanceof Node\Identifier) {
            continue;
        }
        $className = $call->class->toString();
        if ($className !== 'Route' && !str_ends_with($className, '\\Route')) {
            continue;
        }
        $verb = strtolower($call->name->toString());
        if (!in_array($verb, $verbs, true)) {
            continue;
        }
        $args = $call->getArgs();
        if ($args === [] || !$args[0]->value instanceof Node\Scalar\String_) {
            continue;
        }
        $path = $args[0]->value->value;
        $attrs = ['method' => strtoupper($verb), 'path' => $path];
        if (isset($args[1])) {
            $handler = describeHandler($args[1]->value);
            if ($handler['controller'] !== null) {
                $attrs['controller'] = $handler['controller'];
            }
            if ($handler['action'] !== null) {
                $attrs['action'] = $handler['action'];
            }
        }
        $facts[] = [
            'kind' => 'route',
            'id' => "route.$verb.$path",
            'attrs' => $attrs,
            'source' => refFor($call, $relPath),
            'origin' => 'php',
            'certainty' => 'static',
        ];
    }
}

/** @return array{controller: ?string, action: ?string} */
function describeHandler(Node $node): array
{
    // [Controller::class, 'action'] arrays
    if ($node instanceof Node\Expr\Array_ && count($node->items) >= 1) {
        $controller = null;
        $action = null;
        $first = $node->items[0]->value ?? null;
        if ($first instanceof Node\Expr\ClassConstFetch && $first->class instanceof Node\Name) {
            $controller = $first->class->toString();
        }
        $second = $node->items[1]->value ?? null;
        if ($second instanceof Node\Scalar\String_) {
            $action = $second->value;
        }
        return ['controller' => $controller, 'action' => $action];
    }
    // 'Controller@action' strings
    if ($node instanceof Node\Scalar\String_ && str_contains($node->value, '@')) {
        [$controller, $action] = explode('@', $node->value, 2);
        return ['controller' => $controller, 'action' => $action];
    }
    return ['controller' => null, 'action' => null];
}

function collectTables(NodeFinder $finder, array $stmts, string $relPath, array &$facts): void
{
    foreach ($finder->findInstanceOf($stmts, Node\Expr\StaticCall::class) as $call) {
        if (!$call->class instanceof Node\Name || !$call->name instanceof Node\Identifier) {
            continue;
        }
        $className = $call->class->toString();
        if ($className !== 'Schema' && !str_ends_with($className, '\\Schema')) {
            continue;
        }
        if (strtolower($call->name->toString()) !== 'create') {
            continue;
        }
        $args = $call->getArgs();
        if ($args === [] || !$args[0]->value instanceof Node\Scalar\String_) {
            continue;
        }
        $table = $args[0]->value->value;
        $columns = [];
        if (isset($args[1]) && $args[1]->value instanceof Node\Expr\Closure) {
            foreach ($finder->findInstanceOf([$args[1]->value], Node\Expr\MethodCall::class) as $columnCall) {
                if (!$columnCall->name instanceof Node\Identifier) {
                    continue;
                }
                $type = $columnCall->name->toString();
                $columnArgs = $columnCall->getArgs();
                if ($columnArgs !== [] && $columnArgs[0]->value instanceof Node\Scalar\String_) {
                    $columns[] = $columnArgs[0]->value->value . ':' . $type;
                } elseif ($type === 'id') {
                    $columns[] = 'id:id';
                }
            }
        }
        $facts[] = [
            'kind' => 'table',
            'id' => "table.$table",
            'attrs' => ['name' => $table, 'columns' => implode(', ', $columns)],
            'source' => refFor($call, $relPath),
            'origin' => 'php',
            'certainty' => 'static',
        ];
    }
}

function replaceRoutesWithRuntime(string $root, array $facts): array
{
    $artisan = "$root/artisan";
    if (!is_file($artisan) || !is_executable($artisan)) {
        fwrite(STDERR, "extract.php: --boot requested but no executable artisan found, keeping static routes\n");
        return $facts;
    }
    $output = shell_exec('cd ' . escapeshellarg($root) . ' && php artisan route:list --json 2>/dev/null');
    if (!is_string($output) || $output === '') {
        fwrite(STDERR, "extract.php: artisan route:list failed, keeping static routes\n");
        return $facts;
    }
    $routes = json_decode($output, true);
    if (!is_array($routes)) {
        fwrite(STDERR, "extract.php: artisan route:list returned invalid JSON, keeping static routes\n");
        return $facts;
    }

    $facts = array_values(array_filter($facts, static fn ($f) => $f['kind'] !== 'route'));
    foreach ($routes as $route) {
        if (!isset($route['method'], $route['uri'])) {
            continue;
        }
        $verb = strtolower(explode('|', (string) $route['method'])[0]);
        $path = '/' . ltrim((string) $route['uri'], '/');
        $attrs = ['method' => strtoupper($verb), 'path' => $path];
        if (isset($route['action']) && is_string($route['action']) && $route['action'] !== 'Closure') {
            $parts = explode('@', $route['action'], 2);
            $attrs['controller'] = $parts[0];
            if (isset($parts[1])) {
                $attrs['action'] = $parts[1];
            }
        }
        $facts[] = [
            'kind' => 'route',
            'id' => "route.$verb.$path",
            'attrs' => $attrs,
            'source' => ['path' => 'artisan', 'lines' => '1-1'],
            'origin' => 'php',
            'certainty' => 'proved',
        ];
    }
    return $facts;
}
