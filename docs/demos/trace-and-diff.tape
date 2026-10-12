# Charmbracelet VHS Tape — Diverge Diff, Preview Create & Trace Demo
# Run from repository root: vhs docs/demos/trace-and-diff.tape

Output docs/assets/terminal-trace-and-diff.gif

Require diverge

Set Shell "zsh"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1080
Set Height 580
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful

Type "# 1. Detect changed microservices and upstream/ingress impact relative to origin/main"
Enter
Sleep 400ms
Type "diverge diff --config examples/fullstack-demo/.diverge.yaml --base origin/main"
Enter
Sleep 1800ms

Type "# 2. Generate delta PreviewGroup manifest for PR #42 (--dry-run)"
Enter
Sleep 400ms
Type "diverge preview create --name pr-42 --service checkout-api=ghcr.io/acme/checkout-api:pr-42 --service storefront-web --service fulfillment-worker --mr 42 --dry-run"
Enter
Sleep 2200ms

Type "# 3. Trace L7 hop-by-hop routing & state isolation for pr-42"
Enter
Sleep 400ms
Type "diverge trace pr-42 || diverge route checkout-api --config examples/fullstack-demo/.diverge.yaml"
Enter
Sleep 3000ms
