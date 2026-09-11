// Package phpcloak provides pure-Go PHP/Laravel source protection primitives.
//
// It can minify and transform PHP source without invoking a PHP executable and
// can protect or restore complete project trees. Sealed project protection
// still emits a small PHP runtime because the protected Laravel application
// itself ultimately executes under PHP, but phpcloak performs all build-time
// processing in Go.
package phpcloak
