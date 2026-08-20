# poddle terminal demo — regeneration recipe.
#
# The README shows .github/assets/demo.svg, an illustrative reconstruction of the
# flow below (real command syntax + real output shapes), rendered offline with
# svg-term from docs/demo.cast:
#
#   npm i -g svg-term-cli
#   svg-term --in docs/demo.cast --out .github/assets/demo.svg \
#            --window --width 90 --height 20 --padding 18
#
# To record an AUTHENTIC run on a real podman host (produces docs/demo.gif),
# install vhs (https://github.com/charmbracelet/vhs) with ffmpeg + ttyd, then:
#
#   vhs docs/demo.tape
#
# Requires an `anthropic` identity provider and a reachable podman engine.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 20
Set Width 1200
Set Height 660
Set Padding 24
Set Theme "Dracula"

Set TypingSpeed 45ms

Type "poddle identity add work --provider anthropic"
Enter
Sleep 2s

Type `poddle task "add tests for parseConfig" --keep --identity work`
Enter
Sleep 5s

Type "poddle ls"
Enter
Sleep 2s

Type "poddled audit"
Enter
Sleep 3s
