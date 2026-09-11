# phpcloak

`phpcloak` protege código PHP/Laravel usando un motor **100% escrito en Go**. El proceso de minificación, análisis, mangling, cifrado, validación estructural, backup y restauración no ejecuta `php`, no usa `php -w`, no usa `php -l` y no depende de un helper PHP externo.

El proyecto entrega dos superficies sobre el mismo motor:

- **CLI standalone**: `phpcloak` para usarlo desde Windows, Linux o macOS.
- **Librería Go**: `github.com/YahirHub/Laravel-PHP-Obfuscator` para integrarla en otros programas Go.

> `sealed` genera un pequeño runtime PHP porque el proyecto Laravel protegido finalmente se ejecuta bajo PHP. Eso no es una dependencia de compilación ni de ejecución de la herramienta `phpcloak`: el CLI puede proteger un proyecto en una máquina que no tenga PHP instalado. En producción, el Laravel protegido sí necesita PHP y OpenSSL, como corresponde a ese modo.

## Motor Go puro

El motor nativo incluye:

- lexer PHP propio escrito en Go;
- minificador que elimina comentarios y whitespace no necesario;
- validación léxica/estructural de strings, heredoc y delimitadores;
- renombrado conservador de variables locales;
- protección de parámetros, propiedades, superglobales, `$this`, `global` y closure `use`;
- detección conservadora de PHP dinámico para desactivar renombrados riesgosos;
- ofuscación adicional de strings estáticos, métodos y helpers Laravel en modo `aggressive`;
- cifrado AES-256-GCM en `sealed`;
- backup íntegro con SHA-256 y restauración exacta;
- cancelación mediante `context.Context` cuando se usa como librería.

No hay dependencias Go de terceros y no requiere CGO.

## Modos

| Modo | Comportamiento |
| --- | --- |
| `sealed` | Minifica en Go, cifra el payload completo con AES-256-GCM y deja un stub PHP pequeño. Es el modo predeterminado. |
| `aggressive` | Minificación + renombrado conservador de variables locales + ocultamiento de strings estáticos, métodos y helpers Laravel. |
| `strong` | Minificación + renombrado conservador de variables locales. |
| `safe` | Sólo minificación nativa Go. |

Los archivos que usan `__FILE__`, `__DIR__` o `__halt_compiler` no se sellan porque materializarlos temporalmente cambiaría su semántica. En esos casos `sealed` usa automáticamente el fallback `aggressive`.

## Qué procesa por defecto

Incluye:

```text
app/
routes/
modules/
packages/
```

Excluye:

```text
vendor/
node_modules/
storage/
bootstrap/cache/
.git/
resources/views/
public/build/
app/.phpcloak/
.debofuscated/
```

Puedes cambiarlo con `--include`, `--exclude` o `--all-php`.

## Requisitos

### Para compilar phpcloak

- Go 1.24 o superior.
- No requiere PHP.
- No requiere CGO.
- No requiere librerías Go de terceros.

### Para ejecutar phpcloak

Sólo necesitas el binario y permisos de lectura/escritura sobre el proyecto objetivo.

### Para ejecutar un Laravel protegido con `sealed`

El servidor de destino necesita PHP con OpenSSL habilitado, porque el runtime generado descifra el payload antes de ejecutarlo. Esto pertenece al runtime de Laravel, no al motor de construcción de phpcloak.

## Compilar el CLI

Linux/macOS:

```bash
go build -trimpath -ldflags="-s -w" -o phpcloak ./cmd/phpcloak
```

Windows:

```powershell
go build -trimpath -ldflags="-s -w" -o phpcloak.exe ./cmd/phpcloak
```

Cross-compile para Windows AMD64:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o phpcloak.exe ./cmd/phpcloak
```

Comprobar:

```bash
./phpcloak version
./phpcloak --help
```

## Uso del CLI

```bash
phpcloak --root /ruta/a/laravel obfuscate
```

Windows:

```powershell
phpcloak.exe --root "C:\ruta\a\laravel" obfuscate
```

Elegir modo:

```bash
phpcloak --root /srv/app obfuscate --mode sealed
phpcloak --root /srv/app obfuscate --mode aggressive
phpcloak --root /srv/app obfuscate --mode strong
phpcloak --root /srv/app obfuscate --mode safe
```

Opciones:

```text
--mode sealed|aggressive|strong|safe
--include LIST
--exclude LIST
--all-php
--key-file PATH
--key-env NAME
--validate=true|false
--verify=true|false     # alias compatible de --validate
--force
```

Ya no existe `--php`: el motor no necesita un ejecutable PHP.

## Key del modo sealed

Si no existe una key, `phpcloak` crea:

```text
app/.phpcloak/phpcloak.key
```

También crea:

```text
.phpcloak-runtime.php
```

Puedes cambiar la ruta local:

```bash
phpcloak --root /srv/app obfuscate --key-file app/.phpcloak/build.key
```

O usar una variable de entorno como override:

```bash
export MY_PHP_CLOAK_KEY='BASE64_DE_32_BYTES'
phpcloak --root /srv/app obfuscate --key-env MY_PHP_CLOAK_KEY
```

La key representa exactamente 32 bytes codificados en Base64.

## Backup y restauración

Antes de modificar el proyecto se crea:

```text
.debofuscated/
├── backup.json
├── sealed.key      # sólo sealed
└── original/
```

`.debofuscated/original/` contiene el código fuente real. **Nunca despliegues `.debofuscated/` a producción.**

Restaurar:

```bash
phpcloak --root /ruta/a/laravel restore
```

Conservar el backup:

```bash
phpcloak --root /ruta/a/laravel restore --keep-backup
```

La restauración verifica SHA-256 antes de tocar el proyecto y recupera byte por byte los archivos originales.

# Usar phpcloak como librería Go

El módulo raíz es directamente importable. En cada Release el workflow crea también un alias SemVer (`v0.1.0`, `v0.2.0`, etc.) para que el ecosistema de módulos Go pueda resolver versiones de forma estándar. Después de ejecutar el primer Release manual:

```bash
go get github.com/YahirHub/Laravel-PHP-Obfuscator@v0.1.0
```

```go
import phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"
```

## Transformar una fuente PHP en memoria

```go
package main

import (
    "fmt"

    phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"
)

func main() {
    source := []byte("<?php $secret = 'hello'; echo $secret;")

    result, err := phpcloak.Transform(source, phpcloak.ModeAggressive)
    if err != nil {
        panic(err)
    }

    fmt.Println(string(result.Source))
    fmt.Printf("%+v\n", result.Stats)
}
```

`Transform` trabaja con `safe`, `strong` y `aggressive`. Para `sealed` usa `Protect`, porque ese modo necesita conocer rutas del proyecto, runtime y key.

## Minificar y validar

```go
minified, err := phpcloak.Minify(source)
if err != nil {
    panic(err)
}

if err := phpcloak.Validate(minified); err != nil {
    panic(err)
}
```

## Proteger un proyecto completo

```go
package main

import (
    "context"
    "fmt"

    phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"
)

func main() {
    cfg := phpcloak.DefaultConfig("/srv/mi-laravel")
    cfg.Mode = phpcloak.ModeSealed

    manifest, err := phpcloak.Protect(context.Background(), cfg)
    if err != nil {
        panic(err)
    }

    fmt.Printf("protegidos: %d\n", len(manifest.Entries))
}
```

## Restaurar desde Go

```go
result, err := phpcloak.Restore(context.Background(), phpcloak.RestoreOptions{
    Root: "/srv/mi-laravel",
})
if err != nil {
    panic(err)
}

fmt.Println(result.Restored)
```

## API pública principal

```text
DefaultConfig(root string) Config
Protect(ctx context.Context, cfg Config) (*Manifest, error)
Restore(ctx context.Context, opts RestoreOptions) (*RestoreResult, error)
Transform(source []byte, mode Mode) (TransformResult, error)
Minify(source []byte) ([]byte, error)
Validate(source []byte) error
```

# Modelo de seguridad y límites

- `sealed` usa AES-256-GCM con nonce aleatorio por archivo.
- La key predeterminada tiene 32 bytes aleatorios.
- Los originales se guardan antes de transformar cualquier archivo.
- El backup registra SHA-256 y se verifica al restaurar.
- Los archivos se preparan en staging antes de reemplazar el proyecto.
- Si un reemplazo falla, se intenta rollback de lo ya modificado.
- El mangling desactiva el renombrado de variables ante construcciones dinámicas conocidas como `compact`, `extract`, `eval`, `include`, variables variables o interpolación compleja.

La validación nativa actual es **léxica y estructural**, no un compilador PHP completo: detecta strings/comentarios/heredoc incompletos y delimitadores inválidos, y el transformador evita construcciones que no puede cambiar de forma segura. Las pruebas cubren el motor y el CLI sin PHP instalado. Para proyectos que usan extensiones de sintaxis inusuales conviene incorporar fixtures representativos a la suite antes de desplegar.

`sealed` no pretende ser DRM inviolable. Como la aplicación necesita la key para ejecutarse, un administrador con control total del servidor puede recuperar el plaintext con tiempo suficiente. El objetivo es evitar que el código desplegado quede legible o copiable de forma trivial.

# Release automático

El workflow `.github/workflows/release.yml` se ejecuta manualmente desde GitHub Actions. Una vez iniciado no requiere intervención:

1. ejecuta `go test ./...` y `go vet ./...`;
2. calcula automáticamente `v0.1`, `v0.2`, `v0.3`...;
3. reserva también el alias SemVer de módulo Go `v0.1.0`, `v0.2.0`, `v0.3.0`...;
4. compila Windows/Linux/macOS para AMD64 y ARM64;
5. inyecta la versión en el CLI;
6. genera `SHA256SUMS.txt`;
7. crea el tag corto y el GitHub Release;
8. crea el alias SemVer Go apuntando al mismo commit;
9. adjunta todos los binarios.

# Desarrollo

```bash
gofmt -w *.go cmd/phpcloak/*.go
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Prueba explícita de independencia de PHP:

```bash
go build -o /tmp/phpcloak ./cmd/phpcloak
env -i PATH=/definitely-no-php HOME=/tmp \
  /tmp/phpcloak --root /ruta/a/un/fixture obfuscate --mode strong
```
