# TW3 demo. Requires the demo-only seam SPLICE_TUI_DEMO=worktree-reject
# (internal/tui/worktree_demo.go). Unset, the TUI calls splicerun.Run as usual.
# The tape types /exec. Worktree prepare and the Accept/Reject/Keep review
# stay on the real path. Reject pins splice/<name> then removes the worktree.
# If VHS hangs on GIF encode, record an .mp4 and convert with ffmpeg.

Output docs/assets/tui-worktree-reject.gif
Set Shell "bash"
Set FontSize 14
Set Width 1000
Set Height 580
Set Padding 8
Set Framerate 16
Set TypingSpeed 8ms
Set Theme {"name":"splice","black":"#141414","red":"#ef6b6b","green":"#7dcea0","yellow":"#efc34a","blue":"#34e2ea","magenta":"#c79bff","cyan":"#34e2ea","white":"#faf9f6","background":"#141414","foreground":"#faf9f6","cursor":"#34e2ea","brightBlack":"#4a4944","brightRed":"#ef6b6b","brightGreen":"#7dcea0","brightYellow":"#efc34a","brightBlue":"#34e2ea","brightMagenta":"#c79bff","brightCyan":"#34e2ea","brightWhite":"#faf9f6"}

Hide
Type `scripts/tui-worktree-reject-setup.sh`
Enter
Sleep 7s
Show

Type `/exec fix the failing test`
Sleep 400ms
Escape
Sleep 200ms
Enter
Sleep 16s
Up
Sleep 800ms
Enter
Sleep 4s
