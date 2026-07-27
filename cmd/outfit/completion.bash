# bash completion for outfit. Install with:
#
#   source <(outfit completion bash)
#
# or drop this file into /etc/bash_completion.d/ (or, on macOS,
# "$(brew --prefix)/etc/bash_completion.d/").
#
# The candidates come from the binary itself, via the hidden `outfit __complete`
# command: it is handed every word up to the cursor and prints one candidate per
# line, then a directive line saying whether paths belong in the list too.

_outfit() {
  local cur out directive line
  local -a candidates=()

  cur="${COMP_WORDS[COMP_CWORD]}"
  # Call whichever binary is on the command line, not whatever "outfit" happens
  # to resolve to now.
  out="$("${COMP_WORDS[0]}" __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null)"

  directive=":nofile"
  while IFS= read -r line; do
    case "${line}" in
      :*) directive="${line}" ;;
      "") ;;
      *) candidates+=("${line}") ;;
    esac
  done <<< "${out}"

  COMPREPLY=()
  if ((${#candidates[@]})); then
    # Alias names and flags never contain whitespace, so splitting the
    # candidate list on newlines alone is safe.
    local IFS=$'\n'
    COMPREPLY=($(compgen -W "${candidates[*]}" -- "${cur}"))
  fi

  case "${directive}" in
    :file)
      COMPREPLY+=($(compgen -f -- "${cur}"))
      compopt -o filenames 2>/dev/null
      ;;
    # Nothing emits :dir yet. It is handled anyway so a later binary can start
    # using it without everyone re-sourcing this script.
    :dir)
      COMPREPLY+=($(compgen -d -- "${cur}"))
      compopt -o filenames 2>/dev/null
      ;;
  esac
}

complete -F _outfit outfit
