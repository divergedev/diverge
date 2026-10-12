# Charmbracelet VHS Tape — Autonomous AgentTask Sandbox & Live Budget Metering
# Run from repository root: vhs docs/demos/agent-sandbox.tape

Output docs/assets/terminal-agent-sandbox.gif

Require kubectl
Require curl

Set Shell "zsh"
Set FontFamily "JetBrains Mono"
Set FontSize 15
Set Width 1080
Set Height 580
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful

Type "# 1. Dispatch an autonomous AgentTask in an isolated gVisor sandbox with auto-linked preview"
Enter
Sleep 400ms
Type "kubectl apply -f config/samples/agenttask_autonomous.yaml"
Enter
Sleep 1600ms

Type "# 2. Inspect AgentTask status, linked preview environment, and budget guardrails"
Enter
Sleep 400ms
Type "kubectl get agenttask fix-payment-retry-idempotency -o wide"
Enter
Sleep 1800ms

Type "# 3. Report live LLM token & USD usage via Macaroon-authenticated metering endpoint"
Enter
Sleep 400ms
Type `curl -sS -X POST http://localhost:8081/api/v1/agenttask/meter -H "Authorization: Bearer $DIVERGE_MACAROON_TOKEN" -H "Content-Type: application/json" -d '{"namespace":"default","name":"fix-payment-retry-idempotency","tokensDelta":14200,"costUSDDelta":"0.18","iteration":1}' | jq .`
Enter
Sleep 3000ms
