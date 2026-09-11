# Fecha
2026-09-11

# Objetivo
Corregir la regresión detectada al proteger un Laravel real, donde el minificador Go podía fusionar `instanceof` con un nombre de clase iniciado por `\`, y añadir encabezados visibles y multilinea a los PHP protegidos.

# Decisiones tomadas
- Mantener el motor 100% Go y sin depender de PHP CLI para ofuscar.
- Preservar un espacio cuando el código fuente tenía whitespace o comentario entre un identificador y un separador de namespace inicial `\`; esto evita convertir `instanceof \Clase`, `new \Clase` y construcciones equivalentes en tokens distintos.
- Añadir una validación nativa explícita que rechaza `instanceof\Clase` unido.
- Añadir `Config.HeaderText` a la API pública.
- Añadir `--header-text` y `--header-file` al CLI; son mutuamente excluyentes.
- Insertar el encabezado después del primer tag PHP como comentario de bloque, nunca como texto antes de `<?php`, para evitar salida HTTP accidental.
- El encabezado permanece visible también en stubs y runtime de `sealed`; no forma parte del plaintext cifrado.
- Rechazar `*/`, `?>` y NUL en encabezados para impedir que el texto cierre el comentario o el bloque PHP.

# Arquitectura actual
- `transform.go`: el renderizador conserva whitespace semánticamente necesario antes de `\`.
- `lexer.go`: la validación nativa detecta la colisión conocida `instanceof\...`.
- `project.go`: genera y añade comentarios de encabezado seguros a cada PHP protegido y al runtime sealed.
- `types.go`: `Config` expone `HeaderText`.
- `cmd/phpcloak/main.go`: resuelve texto directo con `\n` o contenido desde un archivo UTF-8.

# Librerías usadas
- Sólo Go standard library.
- PHP/OpenSSL sigue siendo requisito únicamente para ejecutar una aplicación protegida en modo `sealed`, no para construirla.

# Archivos importantes modificados
- `types.go`
- `lexer.go`
- `transform.go`
- `project.go`
- `transform_test.go`
- `project_test.go`
- `cmd/phpcloak/main.go`
- `cmd/phpcloak/main_test.go`
- `README.md`
- `contexto/03-correccion-minificador-y-encabezados.md`
- `tareas/03-...`

# Problemas encontrados
- El minificador eliminaba todo whitespace/comentario antes de renderizar y luego decidía separadores sólo mirando los tokens significativos. En PHP 8, `instanceof\Foo` puede tokenizarse de forma distinta a `instanceof \Foo`, produciendo un ParseError.
- La validación estructural anterior comprobaba strings y delimitadores, pero no esta colisión léxica contextual.

# Soluciones implementadas
- `renderTokens` recuerda si hubo un gap entre tokens y conserva un espacio delante de un `\` cuando el token anterior era identificador y originalmente estaban separados.
- `Validate` rechaza la forma unida `instanceof\...` como defensa adicional.
- Pruebas de regresión cubren `instanceof \Illuminate\...` y `new \DateTimeImmutable`.
- Pruebas de encabezado cubren CRLF/LF, salida visible, contenido inseguro, sealed, runtime y restore exacto.
- E2E del CLI ejecutado sin PHP en PATH para modos `safe` y `sealed`, usando `--header-text` y `--header-file`.
- La salida corregida se verificó adicionalmente con PHP 8.3.30 `php -l` en un archivo temporal de Windows y no presentó errores de sintaxis.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...` y `git diff --check` pasan.

# Pendientes
- Repetir la auditoría completa contra el Laravel real una vez que el usuario termine de revertir sus cambios, sin mezclar esa verificación con esta modificación del motor.
- Seguir agregando fixtures reales si aparecen nuevas construcciones sensibles a whitespace.

# Próximos pasos
Compilar un binario Windows AMD64 de prueba, revisar diff final, crear commit en español y publicar `main` sin generar un Release automático.