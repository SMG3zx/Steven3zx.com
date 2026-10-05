param([Parameter(ValueFromRemainingArguments = $true)][string[]] $LinkArgs)

$translated = [System.Collections.Generic.List[string]]::new()
foreach ($argument in $LinkArgs) {
    if ($argument -eq '/NOLOGO') { continue }
    if ($argument.StartsWith('/OUT:', [System.StringComparison]::OrdinalIgnoreCase)) {
        $translated.Add('-o')
        $translated.Add($argument.Substring(5))
        continue
    }
    if ($argument.StartsWith('/DEBUG', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    if ($argument.StartsWith('/NATVIS:', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    if ($argument.StartsWith('/OPT:', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    if ($argument.StartsWith('/PDB', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    if ($argument.StartsWith('/defaultlib:', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
    $translated.Add($argument)
}

& zig cc @translated
exit $LASTEXITCODE
