package phpcloak

import (
	"strings"
	"testing"
)

func TestMinifyPureGo(t *testing.T) {
	source := []byte(`<?php
// comment
#[Route('/demo')]
final class Demo {
    public function run(string $name): string {
        $message = 'hello ' . $name; /* remove */
        return $message;
    }
}
`)
	out, err := Minify(source)
	if err != nil {
		t.Fatalf("Minify: %v", err)
	}
	text := string(out)
	if strings.Contains(text, "comment") || strings.Contains(text, "remove") {
		t.Fatalf("comments were not removed: %s", text)
	}
	if !strings.Contains(text, "#[Route(") {
		t.Fatalf("PHP attribute was damaged: %s", text)
	}
	if err := Validate(out); err != nil {
		t.Fatalf("minified output validation: %v", err)
	}
}

func TestStrongProtectsPublicVariableNames(t *testing.T) {
	source := []byte(`<?php
class Demo {
    private string $name;
    public function run(string $input): string {
        $secret = $input . $this->name;
        return $secret;
    }
}
`)
	res, err := Transform(source, ModeStrong)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	text := string(res.Source)
	for _, want := range []string{"$name", "$input", "$this"} {
		if !strings.Contains(text, want) {
			t.Fatalf("protected variable %s was changed: %s", want, text)
		}
	}
	if strings.Contains(text, "$secret") {
		t.Fatalf("local variable was not renamed: %s", text)
	}
	if res.Stats.VariablesRenamed != 1 {
		t.Fatalf("VariablesRenamed=%d, want 1", res.Stats.VariablesRenamed)
	}
}

func TestDynamicPHPDisablesVariableRename(t *testing.T) {
	source := []byte(`<?php
function demo($input) {
    $local = $input;
    return compact('local');
}
`)
	res, err := Transform(source, ModeStrong)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !res.Stats.RenameDisabled {
		t.Fatal("expected dynamic PHP safeguard to disable variable renaming")
	}
	if !strings.Contains(string(res.Source), "$local") {
		t.Fatalf("local variable changed despite safeguard: %s", res.Source)
	}
}

func TestAggressiveHidesStaticStringsMethodsAndHelpers(t *testing.T) {
	source := []byte(`<?php
function demo($service) {
    $value = 'secret-value';
    $service->publish('topic');
    return view('page', ['value' => $value]);
}
`)
	res, err := Transform(source, ModeAggressive)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	text := string(res.Source)
	for _, plain := range []string{"secret-value", "publish", "topic", "view", "page"} {
		if strings.Contains(text, plain) {
			t.Fatalf("plaintext %q remains in aggressive output: %s", plain, text)
		}
	}
	if res.Stats.StringsHidden < 3 || res.Stats.MethodsHidden != 1 || res.Stats.HelpersHidden != 1 {
		t.Fatalf("unexpected stats: %+v", res.Stats)
	}
	if err := Validate(res.Source); err != nil {
		t.Fatalf("aggressive output validation: %v", err)
	}
}

func TestInterpolatedStringKeepsVariablesStable(t *testing.T) {
	source := []byte("<?php\nfunction demo($name) { $local = 1; return \"Hi $name $local\"; }\n")
	res, err := Transform(source, ModeStrong)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !res.Stats.RenameDisabled {
		t.Fatal("interpolated strings must disable renaming until interpolation rewriting is implemented")
	}
	if !strings.Contains(string(res.Source), "$local") {
		t.Fatalf("interpolated local variable was renamed: %s", res.Source)
	}
}

func TestValidateRejectsBrokenSource(t *testing.T) {
	if err := Validate([]byte("<?php function broken( {")); err == nil {
		t.Fatal("expected validation error")
	}
}
