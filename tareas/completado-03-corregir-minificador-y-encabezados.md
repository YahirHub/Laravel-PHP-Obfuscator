# Corregir minificador y encabezados PHP

## Objetivo
Corregir la regresión del minificador Go que fusiona `instanceof` con nombres de clase iniciados por `\`, y añadir un encabezado de propiedad configurable y multilinea al inicio de cada archivo PHP procesado.

## Alcance
- [x] Añadir prueba de regresión para `instanceof \Namespace` y casos equivalentes detectados en Laravel real.
- [x] Corregir la separación de tokens sin introducir espacios dentro de nombres namespaced válidos.
- [x] Añadir `Config.HeaderText` para la librería Go.
- [x] Añadir `--header-text` con soporte de `\n` y `--header-file` para textos multilinea.
- [x] Insertar el encabezado como comentario PHP seguro y visible después del primer tag PHP.
- [x] Aplicar el encabezado a archivos procesados y al runtime generado en modo `sealed`.
- [x] Rechazar contenido de encabezado que pueda cerrar el comentario o el bloque PHP.
- [x] Añadir pruebas unitarias/E2E de encabezado, sealed, restore y compatibilidad sin PHP CLI.
- [x] Actualizar README y contexto persistente.
- [x] Ejecutar `gofmt`, `go test`, `go test -race`, `go vet` y `go build`.
- [x] Compilar un binario Windows AMD64 de prueba.

## Criterio de terminado
La suite completa pasa, el caso real de `instanceof \...` ya no genera PHP inválido, los encabezados multilinea aparecen de forma segura en los archivos protegidos y `restore` recupera exactamente los originales.