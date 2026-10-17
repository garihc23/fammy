Working directory for the brain's headless Claude Code runs.
Its `.claude/settings.json` switches off the user's global hooks (e.g. a terse
"caveman" output style) so they don't leak into Femi's spoken replies.
Keep this folder stable: `--resume` finds sessions by working directory.
