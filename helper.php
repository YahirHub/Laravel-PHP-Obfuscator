<?php
$source = stream_get_contents(STDIN);
$mode = $argv[1] ?? 'strong';
if ($source === false || $source === '') { fwrite(STDERR, "empty input\n"); exit(2); }
$tokens = token_get_all($source);

$protected = [
    '$this'=>true,'$GLOBALS'=>true,'$_SERVER'=>true,'$_GET'=>true,'$_POST'=>true,'$_FILES'=>true,
    '$_COOKIE'=>true,'$_SESSION'=>true,'$_REQUEST'=>true,'$_ENV'=>true,
];
$disableRename = false;
$riskyTokenIds = [T_EVAL=>true,T_INCLUDE=>true,T_INCLUDE_ONCE=>true,T_REQUIRE=>true,T_REQUIRE_ONCE=>true];
$riskyNames = ['compact'=>true,'extract'=>true,'get_defined_vars'=>true,'parse_str'=>true];

foreach ($tokens as $tok) {
    if (!is_array($tok)) continue;
    [$id,$text] = $tok;
    if (isset($riskyTokenIds[$id])) $disableRename = true;
    if ($id === T_STRING && isset($riskyNames[strtolower($text)])) $disableRename = true;
    if (defined('T_STRING_VARNAME') && $id === T_STRING_VARNAME) $disableRename = true;
    if (defined('T_DOLLAR_OPEN_CURLY_BRACES') && $id === T_DOLLAR_OPEN_CURLY_BRACES) $disableRename = true;
}

// Protect function parameters, closure-use variables, globals and class properties.
$braceDepth = 0;
$classDepths = [];
$functionDepths = [];
$waitingClassBrace = false;
$waitingFunctionParams = false;
$waitingFunctionBrace = false;
$currentIsArrow = false;
$paramDepth = 0;
$inParams = false;
$waitingClosureUse = false;
$useDepth = 0;
$inUse = false;
$inGlobal = false;

foreach ($tokens as $tok) {
    if (is_array($tok)) {
        [$id,$text] = $tok;
        if ($id === T_CLASS || $id === T_TRAIT || $id === T_INTERFACE || (defined('T_ENUM') && $id === T_ENUM)) {
            $waitingClassBrace = true;
        }
        if ($id === T_FUNCTION) {
            $waitingFunctionParams = true;
            $currentIsArrow = false;
            $waitingClosureUse = false;
        } elseif (defined('T_FN') && $id === T_FN) {
            $waitingFunctionParams = true;
            $currentIsArrow = true;
            $waitingClosureUse = false;
        } elseif ($id === T_USE && !$inParams) {
            $waitingClosureUse = true;
        } elseif ($id === T_GLOBAL) {
            $inGlobal = true;
        } elseif ($id === T_VARIABLE) {
            $atClassTop = false;
            if ($classDepths) {
                $cd = $classDepths[count($classDepths)-1];
                $insideFn = $functionDepths && $functionDepths[count($functionDepths)-1] >= $cd;
                $atClassTop = !$insideFn && $braceDepth === $cd;
            }
            if ($inParams || $inUse || $inGlobal || $atClassTop) $protected[$text] = true;
        }
    } else {
        if ($waitingFunctionParams && $tok === '(') {
            $waitingFunctionParams = false; $inParams = true; $paramDepth = 1; continue;
        }
        if ($inParams) {
            if ($tok === '(') $paramDepth++;
            if ($tok === ')') {
                $paramDepth--;
                if ($paramDepth === 0) { $inParams = false; $waitingFunctionBrace = !$currentIsArrow; $currentIsArrow = false; }
            }
            continue;
        }
        if ($waitingClosureUse && $tok === '(') {
            $waitingClosureUse = false; $inUse = true; $useDepth = 1; continue;
        }
        if ($inUse) {
            if ($tok === '(') $useDepth++;
            if ($tok === ')') { $useDepth--; if ($useDepth === 0) $inUse = false; }
            continue;
        }
        if ($inGlobal && $tok === ';') $inGlobal = false;
        if ($tok === '{') {
            $braceDepth++;
            if ($waitingClassBrace) { $classDepths[] = $braceDepth; $waitingClassBrace = false; }
            elseif ($waitingFunctionBrace) { $functionDepths[] = $braceDepth; $waitingFunctionBrace = false; }
        } elseif ($tok === '}') {
            if ($functionDepths && $functionDepths[count($functionDepths)-1] === $braceDepth) array_pop($functionDepths);
            if ($classDepths && $classDepths[count($classDepths)-1] === $braceDepth) array_pop($classDepths);
            $braceDepth--;
        } elseif ($waitingFunctionBrace && $tok === ';') {
            $waitingFunctionBrace = false;
        }
    }
}

function sigPrev(array $tokens, int $i) {
    for ($j=$i-1; $j>=0; $j--) {
        $t=$tokens[$j];
        if (is_array($t) && in_array($t[0],[T_WHITESPACE,T_COMMENT,T_DOC_COMMENT],true)) continue;
        return $t;
    }
    return null;
}
function sigNext(array $tokens, int $i) {
    $n=count($tokens);
    for ($j=$i+1; $j<$n; $j++) {
        $t=$tokens[$j];
        if (is_array($t) && in_array($t[0],[T_WHITESPACE,T_COMMENT,T_DOC_COMMENT],true)) continue;
        return [$j,$t];
    }
    return [null,null];
}
function rawHexString(string $s): string {
    if ($s === '') return '""';
    $o='"';
    for ($i=0,$n=strlen($s); $i<$n; $i++) $o .= sprintf('\\x%02X', ord($s[$i]));
    return $o.'"';
}
function hexString(string $s): string {
    if ($s === '') return '""';
    $mask=''; $round=0;
    while (strlen($mask) < strlen($s)) {
        $mask .= hash('sha256', "phpcloak-v3\\0".$s.pack('N',$round++), true);
    }
    $mask=substr($mask,0,strlen($s));
    $cipher=$s ^ $mask;
    return '('.rawHexString($mask).'^'.rawHexString($cipher).')';
}
function literalValue(string $text): ?string {
    try {
        $v = eval('return '.$text.';');
        return is_string($v) ? $v : null;
    } catch (Throwable $e) { return null; }
}

$methodIndexes=[];
$helperIndexes=[];
$laravelHelpers=array_fill_keys([
    'view','route','redirect','response','request','session','config','app','auth','cache','collect','asset','url',
    'old','now','today','trans','__','abort','abort_if','abort_unless','back','bcrypt','cookie','csrf_field',
    'csrf_token','decrypt','dispatch','encrypt','event','logger','optional','report','rescue','tap','validator','with',
    'data_get','data_set','filled','blank','value','retry','throw_if','throw_unless'
], true);

if ($mode === 'aggressive') {
    foreach ($tokens as $i=>$tok) {
        if (!is_array($tok) || $tok[0] !== T_STRING) continue;
        $prev=sigPrev($tokens,$i);
        [$ni,$next]=sigNext($tokens,$i);
        if ($next !== '(') continue;
        if (is_array($prev) && ($prev[0] === T_OBJECT_OPERATOR || (defined('T_NULLSAFE_OBJECT_OPERATOR') && $prev[0] === T_NULLSAFE_OBJECT_OPERATOR))) {
            $methodIndexes[$i]=true;
            continue;
        }
        $blocked = false;
        if (is_array($prev)) {
            $blocked = in_array($prev[0],[T_FUNCTION,T_NEW,T_DOUBLE_COLON,T_NS_SEPARATOR,T_USE,T_NAMESPACE],true);
        } elseif ($prev === '\\') $blocked = true;
        if (!$blocked && isset($laravelHelpers[strtolower($tok[1])])) $helperIndexes[$i]=true;
    }
}

$map=[]; $counter=0; $out=''; $renamed=0; $strings=0; $methods=0; $helpers=0;
foreach ($tokens as $i=>$tok) {
    if (!is_array($tok)) { $out.=$tok; continue; }
    [$id,$text]=$tok;

    if ($id === T_VARIABLE && !$disableRename && !isset($protected[$text])) {
        if (!isset($map[$text])) {
            $counter++;
            $map[$text]='$_0x'.substr(hash('sha256',$text.':'.$counter.':'.strlen($source)),0,12);
            $renamed++;
        }
        $out.=$map[$text];
        continue;
    }

    if ($mode === 'aggressive' && $id === T_CONSTANT_ENCAPSED_STRING) {
        $v=literalValue($text);
        if ($v !== null) { $out.=hexString($v); $strings++; continue; }
    }

    if ($mode === 'aggressive' && isset($methodIndexes[$i])) {
        $out.='{'.hexString($text).'}'; $methods++; continue;
    }

    if ($mode === 'aggressive' && isset($helperIndexes[$i])) {
        $out.='('.hexString($text).')'; $helpers++; continue;
    }

    $out.=$text;
}

$note='renamed:'.$renamed.';strings:'.$strings.';methods:'.$methods.';helpers:'.$helpers;
if ($disableRename) $note.=';variables:preserved(dynamic-php)';
fwrite(STDERR,$note."\n");
fwrite(STDOUT,$out);
