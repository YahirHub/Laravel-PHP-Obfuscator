package phpcloak

import "testing"

func TestNativeLexerCommonModernPHP(t *testing.T) {
	cases := map[string]string{
		"namespace and attributes": `<?php
namespace App\Demo;
use Attribute;
#[Attribute]
final readonly class Example {
    public function __construct(public string $name) {}
}
`,
		"arrow nullsafe match": `<?php
$fn = fn (?object $item): mixed => $item?->value();
return match ($fn(null)) {
    null => 'none',
    default => 'value',
};
`,
		"heredoc": `<?php
$value = <<<TEXT
hello $name
TEXT;
echo $value;
`,
		"nowdoc": `<?php
$value = <<<'TEXT'
hello $name
TEXT;
echo $value;
`,
		"inline html": `<html><?php if ($ok): ?>yes<?php else: ?>no<?php endif; ?></html>`,
		"enum": `<?php
enum Status: string {
    case Ready = 'ready';
    case Done = 'done';
}
`,
	}

	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Validate([]byte(source)); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			minified, err := Minify([]byte(source))
			if err != nil {
				t.Fatalf("Minify: %v", err)
			}
			if err := Validate(minified); err != nil {
				t.Fatalf("Validate(minified): %v", err)
			}
		})
	}
}

func TestUnterminatedHeredocRejected(t *testing.T) {
	source := []byte("<?php\n$value = <<<TEXT\nhello\n")
	if err := Validate(source); err == nil {
		t.Fatal("expected unterminated heredoc to fail validation")
	}
}
