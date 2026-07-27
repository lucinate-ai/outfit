#compdef outfit
# zsh completion for outfit. Install with:
#
#   source <(outfit completion zsh)
#
# or place it on your $fpath as a file named _outfit — Homebrew does this for
# you — and make sure `autoload -U compinit && compinit` runs from your ~/.zshrc.
#
# The candidates come from the binary itself, via the hidden `outfit __complete`
# command: it is handed every word up to the cursor and prints one candidate per
# line, then a directive line saying whether paths belong in the list too.

_outfit() {
  local line directive out
  local -a candidates

  # $words is the command line (1-indexed; $words[1] is the binary) and $CURRENT
  # is the index of the word under the cursor. Hand __complete everything after
  # the binary up to and including that word, exactly as the bash script does.
  # (@) keeps an empty word-under-cursor as its own argument.
  out="$(${words[1]} __complete "${(@)words[2,$CURRENT]}" 2>/dev/null)"

  directive=":nofile"
  while IFS= read -r line; do
    case "${line}" in
      :*) directive="${line}" ;;
      "") ;;
      *) candidates+=("${line}") ;;
    esac
  done <<< "${out}"

  # For a --flag=value word, only the part after "=" is matched and inserted;
  # compset moves the "--flag=" into the ignored prefix so compadd fills the tail.
  compset -P '*='

  case "${directive}" in
    :file) _files ;;
    :dir) _files -/ ;;
  esac
  (( ${#candidates} )) && compadd -a candidates
}

# Support both `source <(outfit completion zsh)` and being autoloaded from
# $fpath. Autoloaded, the #compdef tag registers us and zsh calls the file as
# the completion function; sourced, we register ourselves.
if [ "${funcstack[1]}" = "_outfit" ]; then
  _outfit "$@"
else
  compdef _outfit outfit
fi
