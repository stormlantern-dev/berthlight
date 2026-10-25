# berthlight completion for nushell, from `berthlight completion nushell`.
#
# An external completer: for berthlight it asks `berthlight __complete`, as
# the bash, zsh and fish scripts do; every other command goes to the
# external completer that was set before this file ran (carapace's, say).
# Save it in an autoload directory, which nushell runs after config.nu:
#
#   berthlight completion nushell | save -f ($nu.user-autoload-dirs | first | path join berthlight.nu)

let berthlight_completer = {|spans: list<string>|
    let lines = (^berthlight __complete ...($spans | skip 1) | complete | get stdout | lines)
    # The last line is :<directive>, a bit set: 1 error, 4 no file
    # completion, 8 and 16 file and directory completion, which nushell
    # does itself.
    let directive = ($lines | where {|l| $l =~ '^:\d+$' } | last | default ':0' | str substring 1.. | into int)
    let rows = ($lines | where {|l| $l !~ '^:\d+$' and $l != '' and not ($l | str starts-with '_activeHelp_') })
    if ($directive bit-and 1) != 0 or ($directive bit-and 24) != 0 {
        null
    } else if ($rows | is-empty) {
        if ($directive bit-and 4) != 0 { [] } else { null }
    } else {
        $rows | each {|l|
            let parts = ($l | split row "\t")
            if ($parts | length) > 1 {
                {value: $parts.0, description: $parts.1}
            } else {
                {value: $parts.0}
            }
        }
    }
}

let berthlight_next_completer = $env.config?.completions?.external?.completer?

$env.config.completions.external.enable = true
# Nushell 0.116 hands an external completer one record and binds its
# fields by parameter name ({|place| ...}, with $place.command the tokens);
# older versions pass the token list. Take either.
$env.config.completions.external.completer = {|place|
    let spans = if ($place | describe | str starts-with 'list') { $place } else { $place.command }
    if ($spans.0 | path parse | get stem) == 'berthlight' {
        do $berthlight_completer $spans
    } else if $berthlight_next_completer != null {
        # The completer set before this one may take the record or, in
        # the older form, the token list.
        try { do $berthlight_next_completer $place } catch { do $berthlight_next_completer $spans }
    }
}
