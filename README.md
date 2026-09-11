# phpcloak

`phpcloak` es una herramienta CLI escrita en Go para proteger código PHP de proyectos Laravel antes de desplegarlo a producción.

Trabaja **in-place** sobre una copia del proyecto: crea un backup de los PHP originales, transforma los archivos seleccionados y deja el proyecto listo para ejecutarse con el código protegido. También puede restaurar exactamente los originales usando el backup generado.

> No sustituye controles de acceso, permisos del servidor ni una solución DRM/HSM. En el modo predeterminado la key de ejecución vive en el mismo servidor que la aplicación para que Laravel pueda arrancar sin un servicio externo. El objetivo es evitar que el código desplegado quede legible de forma trivial.

## Cómo funciona

El flujo de `obfuscate` es:

1. Recorre los PHP incluidos y aplica las exclusiones configuradas.
2. Guarda una copia byte por byte de cada original en `.debofuscated/original/`.
3. Minifica cada PHP con `php -w`.
4. Según el modo elegido:
   - `sealed`: cifra el archivo completo con AES-256-GCM y deja un stub PHP pequeño.
   - `aggressive`: minifica, renombra variables locales seguras y ofusca strings/métodos/helpers seleccionados.
   - `strong`: minifica y renombra variables locales de forma conservadora.
   - `safe`: solo minifica.
5. Valida con `php -l` antes de reemplazar los originales, salvo que se use `--verify=false`.
6. Escribe `.phpcloak-manifest.json` con hashes y acciones realizadas.

En modo `sealed`, cada stub carga `.phpcloak-runtime.php`. Ese runtime lee la key local, descifra el payload con OpenSSL, lo materializa temporalmente, ejecuta el PHP y elimina el temporal al terminar.

Los archivos que usan `__FILE__`, `__DIR__` o `__halt_compiler` no se sellan porque un archivo temporal cambiaría su semántica de rutas. En esos casos se usa automáticamente el fallback `aggressive`.

## Qué procesa por defecto

Se incluyen estas raíces:

```text
app/
routes/
modules/
packages/
```

Se excluyen por defecto:

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

Puedes cambiarlo con `--include`, `--exclude` o usar `--all-php`.

## Requisitos

### Para compilar

- Go **1.24 o superior**.
- No requiere librerías Go de terceros.
- No requiere CGO.

### Para usarlo sobre un proyecto PHP/Laravel

- PHP CLI disponible como `php`, o indicar otro ejecutable con `--php`.
- OpenSSL habilitado en PHP si se usa `sealed`.
- Permisos de lectura/escritura sobre el proyecto objetivo.

Comprobaciones útiles:

```bash
go version
php -v
php -r 'echo extension_loaded("openssl") ? "openssl: ok\n" : "openssl: missing\n";'
```

En Windows:

```powershell
php -v
php -r "echo extension_loaded('openssl') ? 'openssl: ok' : 'openssl: missing';"
```

## Compilar

Build normal:

```bash
go build -o phpcloak .
```

Build optimizado para distribución:

```bash
go build -trimpath -ldflags="-s -w" -o phpcloak .
```

Windows desde Windows:

```powershell
go build -trimpath -ldflags="-s -w" -o phpcloak.exe .
```

Cross-compile para Windows AMD64 desde Linux/macOS:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o phpcloak.exe .
```

Comprobar el binario:

```bash
./phpcloak version
./phpcloak --help
```

## Uso básico

Hazlo sobre una copia de trabajo o justo antes de generar tu release de producción.

Linux/macOS:

```bash
./phpcloak --root /ruta/a/laravel obfuscate
```

Windows:

```powershell
phpcloak.exe --root "C:\ruta\a\laravel" obfuscate
```

El modo predeterminado es `sealed`.

## Key del modo sealed

Si no existe una key, `phpcloak` crea automáticamente:

```text
app/.phpcloak/phpcloak.key
```

También genera:

```text
.phpcloak-runtime.php
```

La key local debe acompañar al build protegido en producción. No depende de `storage/`, por lo que no se pierde al limpiar caches o archivos temporales de Laravel.

Puedes cambiar su ubicación:

```bash
./phpcloak --root /srv/app obfuscate --key-file app/.phpcloak/build.key
```

También existe un override opcional por variable de entorno:

```bash
export MY_PHP_CLOAK_KEY='BASE64_DE_32_BYTES'
./phpcloak --root /srv/app obfuscate --key-env MY_PHP_CLOAK_KEY
```

La key debe representar exactamente 32 bytes codificados en Base64.

## Backup y restauración

Antes de tocar los PHP originales se crea:

```text
.debofuscated/
├── backup.json
├── sealed.key
└── original/
```

`.debofuscated/original/` contiene el código fuente real sin ofuscar.

**Nunca despliegues `.debofuscated/` a producción.**

Restaurar:

```bash
./phpcloak --root /ruta/a/laravel restore
```

La restauración:

- verifica SHA-256 de los backups;
- restaura los PHP originales;
- elimina `.phpcloak-runtime.php`;
- elimina la key local usada por el build;
- elimina `.phpcloak-manifest.json`;
- elimina `.debofuscated/` al finalizar.

Para conservar el backup:

```bash
./phpcloak --root /ruta/a/laravel restore --keep-backup
```

## Modos

| Modo | Descripción |
| --- | --- |
| `sealed` | AES-256-GCM de archivos completos. Predeterminado y el que más oculta el fuente. |
| `aggressive` | Minificación + renombrado seguro de variables locales + ofuscación adicional de strings/métodos/helpers. |
| `strong` | Minificación + renombrado conservador de variables locales. |
| `safe` | Solo `php -w`; menor riesgo y menor protección. |

Ejemplos:

```bash
./phpcloak --root /srv/app obfuscate --mode sealed
./phpcloak --root /srv/app obfuscate --mode aggressive
./phpcloak --root /srv/app obfuscate --mode strong
./phpcloak --root /srv/app obfuscate --mode safe
```

## Opciones principales

```text
--mode sealed|aggressive|strong|safe
--include LIST
--exclude LIST
--all-php
--php PATH
--key-file PATH
--key-env NAME
--verify=true|false
--force
```

Ejemplos:

```bash
# Solo app y routes
./phpcloak --root /srv/app obfuscate --include app,routes

# Añadir una exclusión
./phpcloak --root /srv/app obfuscate --exclude app/Legacy

# Procesar todos los PHP salvo exclusiones
./phpcloak --root /srv/app obfuscate --all-php

# PHP instalado en otra ruta
./phpcloak --root /srv/app obfuscate --php /usr/bin/php83
```

## Archivos generados

Después de un build `sealed` normalmente quedan:

```text
.phpcloak-runtime.php
.phpcloak-manifest.json
app/.phpcloak/phpcloak.key
.debofuscated/
```

Para producción conserva:

```text
.phpcloak-runtime.php
.phpcloak-manifest.json   # opcional para auditoría
app/.phpcloak/phpcloak.key
app/
routes/
modules/
packages/
```

No despliegues:

```text
.debofuscated/
```

## Flujo recomendado de release

```bash
# 1. partir de una copia limpia del proyecto
cp -a mi-laravel mi-laravel-release

# 2. instalar dependencias / preparar Laravel como acostumbras
cd mi-laravel-release
composer install --no-dev --optimize-autoloader
php artisan config:cache
php artisan route:cache

# 3. proteger el código al final del build
/path/phpcloak --root "$PWD" obfuscate

# 4. eliminar del artefacto de despliegue el backup con originales
rm -rf .debofuscated
```

Si quieres conservar capacidad de `restore`, guarda `.debofuscated/` fuera del servidor de producción antes de eliminarlo del artefacto.

## Modelo de seguridad y límites

- `sealed` usa AES-256-GCM con nonce aleatorio por archivo.
- La key predeterminada tiene 32 bytes aleatorios y se almacena en Base64.
- El runtime requiere OpenSSL para descifrar.
- El proyecto hace staging y lint antes de sustituir los PHP originales.
- Si falla un reemplazo, intenta rollback de los archivos ya modificados.
- El backup incluye hashes SHA-256 y se valida antes de restaurar.

La key y el runtime están en la misma máquina porque PHP necesita ejecutar el código. Por eso un administrador con acceso total al servidor y tiempo suficiente puede recuperar el plaintext. La herramienta está orientada a **proteger el fuente desplegado frente a lectura/copia casual o análisis trivial**, no a impedir técnicamente que el dueño de la máquina ejecute ingeniería inversa.

## Desarrollo

Formatear y validar:

```bash
gofmt -w main.go
go test ./...
go vet ./...
go build ./...
```

`helper.php` está embebido en el binario mediante `//go:embed`, así que debe estar presente al compilar pero no necesita distribuirse junto al ejecutable final.
