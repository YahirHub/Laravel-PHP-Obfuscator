# Fecha
2026-09-11

# Objetivo
Publicar el código fuente de phpcloak como repositorio privado y dejar documentación suficiente para compilarlo, entender su modelo de protección y usarlo sobre proyectos Laravel/PHP.

# Decisiones tomadas
- Mantener la herramienta como CLI Go sin dependencias externas de Go.
- PHP CLI sigue siendo dependencia de ejecución para minificar, transformar y validar PHP.
- El modo predeterminado `sealed` usa AES-256-GCM y una key local dentro del proyecto protegido.
- La documentación debe advertir que `.debofuscated/` contiene código fuente original y nunca debe desplegarse.
- No se versionarán binarios compilados.

# Arquitectura actual
- `main.go`: CLI, selección de archivos, backup/restore, sellado AES-256-GCM, runtime y manifiestos.
- `helper.php`: transformación de tokens para modos `strong` y `aggressive`.
- `go.mod`: módulo Go sin dependencias de terceros.
- `README.md`: documentación de build, requisitos, funcionamiento y uso.

# Librerías usadas
- Go standard library.
- PHP CLI en tiempo de ejecución.
- OpenSSL de PHP para ejecutar payloads del modo `sealed`.

# Archivos importantes modificados
- `README.md`
- `.gitignore`
- `contexto/01-contexto-inicial.md`
- `tareas/en-proceso-01-publicar-repositorio.md`

# Problemas encontrados
- El sandbox de desarrollo no tiene PHP CLI instalado actualmente, por lo que aquí se puede validar la compilación Go, pero no ejecutar una prueba end-to-end de ofuscación PHP en esta entrega.
- La creación del repositorio remoto requiere autorizar la cuenta personal de GitHub en Integraciones > GitHub.

# Soluciones implementadas
- Documentación explícita de requisitos, funcionamiento, compilación, uso, deployment y límites de seguridad.
- `.gitignore` para evitar publicar binarios y temporales.
- `go test`, `go vet`, build y `phpcloak version` validados correctamente.

# Pendientes
- Ejecutar una prueba end-to-end en un entorno con PHP CLI si se modifica la lógica de ofuscación en el futuro.

# Próximos pasos
Crear el repositorio privado, hacer el primer commit y subir la rama `main`.
