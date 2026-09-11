# Fecha
2026-09-11

# Objetivo
Convertir phpcloak en un motor de protección PHP/Laravel 100% ejecutado en Go, eliminar la dependencia de PHP CLI durante el proceso de build/ofuscación y exponer el mismo motor como librería Go reutilizable.

# Decisiones tomadas
- La raíz del módulo es ahora el paquete importable `phpcloak`.
- El CLI vive en `cmd/phpcloak` y consume la misma API pública que cualquier aplicación externa.
- El módulo canónico es `github.com/YahirHub/Laravel-PHP-Obfuscator`.
- No se usan dependencias Go de terceros ni CGO.
- Se eliminó `helper.php`, `os/exec`, `php -w`, `php -l` y el flag `--php`.
- `--verify` se conserva como alias compatible de `--validate`.
- La validación nativa es léxica/estructural; no pretende ser un compilador PHP completo.
- El modo `sealed` sigue generando runtime PHP porque el Laravel protegido finalmente corre bajo PHP; la herramienta de construcción no necesita PHP instalado.
- Los Releases conservan el esquema solicitado `v0.1`, `v0.2`, etc. y además generan un alias SemVer para módulos Go (`v0.1.0`, `v0.2.0`, etc.) en el mismo commit.

# Arquitectura actual
- `types.go`: API pública, modos, configuración, manifiestos y resultados.
- `lexer.go`: lexer PHP nativo y validación estructural.
- `transform.go`: minificación, mangling conservador y ocultamiento aggressive.
- `project.go`: protección de árboles de proyecto, AES-256-GCM, staging, backup y restore.
- `cmd/phpcloak/main.go`: interfaz CLI fina sobre la librería.
- `.github/workflows/release.yml`: pruebas, builds multiplataforma, tag corto, Release y alias SemVer Go.

# Librerías usadas
- Exclusivamente Go standard library.
- En runtime del proyecto Laravel protegido con `sealed`, PHP necesita OpenSSL para descifrar AES-256-GCM. Eso no forma parte de las dependencias del ejecutable phpcloak.

# Archivos importantes modificados
- `go.mod`
- `types.go`
- `lexer.go`
- `transform.go`
- `project.go`
- `doc.go`
- `cmd/phpcloak/main.go`
- `lexer_test.go`
- `transform_test.go`
- `project_test.go`
- `example_test.go`
- `README.md`
- `.github/workflows/release.yml`
- eliminado: `helper.php`

# Problemas encontrados
- Una subcarpeta llamada `phpcloak/` chocaba con el nombre natural del binario y con la regla `/phpcloak` de `.gitignore`; se descartó esa estructura.
- Los tags cortos `v0.1`, `v0.2` no son la forma SemVer ideal para consumidores de módulos Go, por lo que se añadió un alias automático `v0.N.0` sin cambiar el nombre visible del Release solicitado.
- Una validación 100% Go sin un parser PHP completo no puede afirmar equivalencia con `php -l` para toda extensión futura del lenguaje; se documenta este límite y se priorizan transformaciones conservadoras.

# Soluciones implementadas
- Lexer propio que reconoce tags PHP, inline HTML, comentarios, strings, heredoc/nowdoc, variables, identificadores, números y operadores modernos.
- Minificador Go con separación conservadora para evitar fusionar tokens accidentalmente.
- Validador de strings/heredoc y delimitadores balanceados.
- Protección de parámetros, propiedades, `$this`, superglobales, variables `global` y captures de closures.
- Renombrado desactivado ante PHP dinámico conocido (`eval`, include/require, `compact`, `extract`, variables variables e interpolación).
- API pública: `DefaultConfig`, `Protect`, `Restore`, `Transform`, `Minify` y `Validate`.
- E2E real del CLI ejecutado con `PATH=/definitely-no-php`, validando aggressive, sealed y restore exacto por SHA-256.
- Pruebas de sintaxis moderna: attributes, namespace, readonly class, arrow functions, nullsafe, match, heredoc, nowdoc, inline HTML y enums.
- `go test ./...`, `go test -race ./...`, `go vet ./...` y `go build ./...` pasan.

# Pendientes
- Seguir incorporando fixtures reales de proyectos Laravel si aparecen construcciones PHP no cubiertas por el lexer nativo.
- Evaluar un parser PHP completo escrito en Go sólo si los fixtures reales demuestran que la validación estructural es insuficiente; no agregar esa dependencia de forma especulativa.

# Publicación
- `43bc105` — `Convertir phpcloak a motor Go puro y librería`.
- `65c46e9` — `Adaptar releases al motor Go puro`.
- Ambos cambios están publicados en `main` de `YahirHub/Laravel-PHP-Obfuscator`.

# Próximos pasos
Cuando se quiera publicar la primera versión del motor Go puro, ejecutar manualmente **Actions → Manual Release**. El workflow creará `v0.1` para el Release y `v0.1.0` como alias SemVer del módulo Go, sin intervención adicional. No se ejecutó el workflow durante esta tarea.
