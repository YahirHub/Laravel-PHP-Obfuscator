package phpcloak_test

import (
	"context"
	"fmt"

	phpcloak "github.com/YahirHub/Laravel-PHP-Obfuscator"
)

func ExampleTransform() {
	source := []byte("<?php $message = 'hello'; echo $message;")
	result, err := phpcloak.Transform(source, phpcloak.ModeStrong)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(result.Source) > 0, result.Stats.VariablesRenamed > 0)
	// Output: true true
}

func ExampleDefaultConfig() {
	cfg := phpcloak.DefaultConfig("/srv/laravel")
	cfg.Mode = phpcloak.ModeAggressive
	_, _ = phpcloak.Protect(context.Background(), cfg)
	fmt.Println(cfg.Mode)
	// Output: aggressive
}
