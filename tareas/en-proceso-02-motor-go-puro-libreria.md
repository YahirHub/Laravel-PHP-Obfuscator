# Motor 100% Go y librería reutilizable

## Objetivo
Eliminar la dependencia del ejecutable `php` y convertir el núcleo de phpcloak en un paquete Go importable por otros proyectos.

## Alcance
- [ ] Mover el CLI a `cmd/phpcloak` y dejar la raíz del módulo como paquete `phpcloak`.
- [ ] Cambiar el módulo a `github.com/YahirHub/Laravel-PHP-Obfuscator`.
- [ ] Implementar lexer/minificador/validador de PHP en Go sin procesos externos.
- [ ] Portar el mangling de variables, strings, métodos y helpers Laravel a Go.
- [ ] Mantener modos `sealed`, `aggressive`, `strong` y `safe`.
- [ ] Mantener backup/restore y runtime sealed existentes.
- [ ] Exponer API pública Go para transformar fuentes y proteger/restaurar proyectos.
- [ ] Eliminar `helper.php` y cualquier uso de `os/exec` para PHP.
- [ ] Añadir pruebas unitarias y E2E sin PHP instalado.
- [ ] Actualizar README, contexto y workflow de release.
- [ ] Crear commit en español y publicar en `main`.
