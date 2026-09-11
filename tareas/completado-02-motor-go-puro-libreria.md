# Motor 100% Go y librería reutilizable

## Objetivo
Eliminar la dependencia del ejecutable `php` y convertir el núcleo de phpcloak en un paquete Go importable por otros proyectos.

## Estado
Completada y publicada en `main`.

## Alcance
- [x] Mover el CLI a `cmd/phpcloak` y dejar la raíz del módulo como paquete `phpcloak`.
- [x] Cambiar el módulo a `github.com/YahirHub/Laravel-PHP-Obfuscator`.
- [x] Implementar lexer/minificador/validador de PHP en Go sin procesos externos.
- [x] Portar el mangling de variables, strings, métodos y helpers Laravel a Go.
- [x] Mantener modos `sealed`, `aggressive`, `strong` y `safe`.
- [x] Mantener backup/restore y runtime sealed existentes.
- [x] Exponer API pública Go para transformar fuentes y proteger/restaurar proyectos.
- [x] Eliminar `helper.php` y cualquier uso de `os/exec` para PHP.
- [x] Añadir pruebas unitarias y E2E sin PHP instalado.
- [x] Actualizar README, contexto y workflow de release.
- [x] Crear commits en español y publicar en `main`.

## Validación
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- E2E del CLI con `PATH=/definitely-no-php` para `aggressive`, `sealed` y `restore` exacto por SHA-256.
- Consumo desde un módulo Go externo usando `import phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"`.
- Workflow validado para `v0.1`, `v0.2`... y alias SemVer `v0.1.0`, `v0.2.0`...

## Publicación
- `43bc105` — `Convertir phpcloak a motor Go puro y librería`.
- `65c46e9` — `Adaptar releases al motor Go puro`.

El workflow no se ejecutó: la creación del Release permanece manual, tal como se solicitó.
