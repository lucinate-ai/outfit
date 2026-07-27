# PowerShell completion for outfit. Install with:
#
#   outfit completion powershell | Out-String | Invoke-Expression
#
# Add that line to your profile ($PROFILE) to load it in every session.
#
# The candidates come from the binary itself, via the hidden `outfit __complete`
# command: it is handed every word up to the cursor and prints one candidate per
# line, then a directive line saying whether paths belong in the list too.

Register-ArgumentCompleter -Native -CommandName outfit -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    # Everything after the binary, up to and including the word under the cursor.
    # When completing a fresh word there is a trailing space, so $wordToComplete
    # is empty and the parsed elements stop one short — add the empty word back.
    $elements = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.ToString() })
    if ([string]::IsNullOrEmpty($wordToComplete)) {
        $elements += ''
    }

    $exe = $commandAst.CommandElements[0].Value
    $out = & $exe __complete @elements 2>$null

    $candidates = @()
    foreach ($line in $out) {
        if ($line -like ':*' -or $line -eq '') { continue }
        $candidates += $line
    }

    # For a --flag=value word, candidates fill in only the part after "="; the
    # inserted text carries the "--flag=" back so the whole word stays intact.
    $prefix = ''
    $value = $wordToComplete
    if ($wordToComplete -match '^(--?[^=]*=)(.*)$') {
        $prefix = $Matches[1]
        $value = $Matches[2]
    }

    $results = @()
    foreach ($c in $candidates) {
        if ($c -like "$value*") {
            $results += [System.Management.Automation.CompletionResult]::new(
                $prefix + $c, $c, 'ParameterValue', $c)
        }
    }

    # With no matching candidate, return nothing so PowerShell falls back to its
    # own path completion — the file directives (:file/:dir) resolve to that.
    $results
}
