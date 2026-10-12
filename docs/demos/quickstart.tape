# Charmbracelet VHS Tape — Diverge Quickstart Demo
# Run from repository root: vhs docs/demos/quickstart.tape

Output docs/assets/terminal-quickstart-demo.gif

Require diverge

Set Shell "zsh"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1040
Set Height 560
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set WindowBar Colorful

Type "# 1. Scaffold .diverge.yaml with JSON Schema validation header"
Enter
Sleep 400ms
Type "diverge init --config --output /tmp/diverge-quickstart.yaml --force"
Enter
Sleep 1500ms

Type "# 2. Validate .diverge.yaml against embedded JSON Schema"
Enter
Sleep 400ms
Type "diverge validate --config /tmp/diverge-quickstart.yaml"
Enter
Sleep 1500ms

Type "# 3. Preview local Kubernetes playground bootstrap (k3d/Kind + Envoy Gateway + Diverge)"
Enter
Sleep 400ms
Type "diverge quickstart --local --dry-run || diverge init --provider kind --dry-run"
Enter
Sleep 3000ms
