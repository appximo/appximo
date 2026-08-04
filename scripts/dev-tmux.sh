#!/usr/bin/env bash
SESSION="appximo-dev"
if tmux has-session -t "$SESSION" 2>/dev/null; then
  tmux attach-session -t "$SESSION"; exit 0
fi
tmux new-session -d -s "$SESSION" -n "dev" -x 220 -y 55
tmux split-window -t "$SESSION:0" -v -p 50
tmux select-pane  -t "$SESSION:0.0"
tmux split-window -t "$SESSION:0.0" -h -p 50
tmux split-window -t "$SESSION:0.2" -v -p 50
tmux send-keys -t "$SESSION:0.0" 'make test-watch' Enter
tmux send-keys -t "$SESSION:0.1" 'make devhub-run' Enter
tmux send-keys -t "$SESSION:0.2" 'echo "Panel k6: make test-perf-dashboard"' Enter
tmux send-keys -t "$SESSION:0.3" 'echo "Panel libre"' Enter
tmux attach-session -t "$SESSION"
