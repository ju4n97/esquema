#!/usr/bin/env bash
set -euo pipefail

TAG=""
SKIP_CHECKS=false

# Parse flags and arguments
for arg in "$@"; do
  case "$arg" in
    --skip-checks) SKIP_CHECKS=true ;;
    *) TAG="$arg" ;;
  esac
done

CLR_RED="\033[31m"
CLR_GREEN="\033[32m"
CLR_YELLOW="\033[33m"
CLR_BLUE="\033[34m"
CLR_CYAN="\033[36m"
CLR_BOLD="\033[1m"
CLR_RESET="\033[0m"

err()  { echo -e "${CLR_RED}[ERROR]${CLR_RESET} $*" >&2; exit 1; }
info() { echo -e "${CLR_BLUE}[INFO]${CLR_RESET} $*"; }
ok()   { echo -e "${CLR_GREEN}[OK]${CLR_RESET} $*"; }
warn() { echo -e "${CLR_YELLOW}[WARN]${CLR_RESET} $*"; }
step() { echo -e "${CLR_CYAN}[RUN]${CLR_RESET} $*"; }

# Validation: Tag argument provided
if [[ -z "$TAG" ]]; then
  err "Missing version tag argument.\nUsage: task tag -- v0.1.0  (or ./scripts/tag.sh v0.1.0 [--skip-checks])"
fi

# Validation: Strict SemVer format with leading 'v'
SEMVER_REGEX="^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$"
if [[ ! "$TAG" =~ $SEMVER_REGEX ]]; then
  err "Invalid version format: '${CLR_BOLD}${TAG}${CLR_RESET}'.\nTag must follow Semantic Versioning with a leading 'v' (e.g. v0.1.0, v1.0.0-rc.1)."
fi

# Resolve Task runner binary
TASK_CMD=""
if command -v task >/dev/null 2>&1; then
  TASK_CMD="task"
elif command -v go-task >/dev/null 2>&1; then
  TASK_CMD="go-task"
elif [[ -x "$HOME/go/bin/task" ]]; then
  TASK_CMD="$HOME/go/bin/task"
elif [[ -x "$HOME/.local/bin/task" ]]; then
  TASK_CMD="$HOME/.local/bin/task"
elif [[ -x "/usr/bin/go-task" ]]; then
  TASK_CMD="/usr/bin/go-task"
fi

# Validation: Task runner available
if [[ -z "$TASK_CMD" ]]; then
  err "Task runner not found. Install Task (https://taskfile.dev/) before releasing."
fi

# Auto-generate CLI documentation and man pages
if [ "$SKIP_CHECKS" = false ]; then
  info "Synchronizing CLI documentation and man pages..."
  "$TASK_CMD" gen
fi

# Validation: Working directory clean (checks if docs or code were modified)
if [[ -n "$(git status --porcelain)" ]]; then
  err "Working directory has uncommitted changes or generated documentation was out of date.\nReview and commit the changes:\n  git commit -am 'docs(cli): update generated CLI reference and man page'\nbefore releasing."
fi

# Validation: Current Git branch
CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")"
if [[ "$CURRENT_BRANCH" != "main" && "$CURRENT_BRANCH" != "master" ]]; then
  warn "You are on branch '${CLR_BOLD}${CURRENT_BRANCH}${CLR_RESET}', not 'main'."
  read -rp "Do you really want to release from '${CURRENT_BRANCH}'? [y/N] " confirm
  [[ "$confirm" =~ ^[Yy]$ ]] || err "Release aborted."
fi

# Validation: Synchronized with remote
info "Verifying remote sync with origin/${CURRENT_BRANCH}..."
git fetch origin "$CURRENT_BRANCH" --quiet 2>/dev/null || true

LOCAL_SHA="$(git rev-parse HEAD)"
REMOTE_SHA="$(git rev-parse "origin/$CURRENT_BRANCH" 2>/dev/null || echo "$LOCAL_SHA")"

if [[ "$LOCAL_SHA" != "$REMOTE_SHA" ]]; then
  err "Local branch '${CURRENT_BRANCH}' is not in sync with origin. Pull or push your commits first."
fi

# Validation: Tag collision check (local and remote)
if git rev-parse "$TAG" >/dev/null 2>&1; then
  err "Tag '${CLR_BOLD}${TAG}${CLR_RESET}' already exists locally."
fi

if git ls-remote --tags origin | grep -q "refs/tags/${TAG}$"; then
  err "Tag '${CLR_BOLD}${TAG}${CLR_RESET}' already exists on remote 'origin'."
fi

# Automated test and lint verification suite
if [ "$SKIP_CHECKS" = false ]; then
  echo
  info "Executing pre-release test and lint verification suite:"
  
  step "1/4 Running linters ($TASK_CMD lint)..."
  "$TASK_CMD" lint
  
  step "2/4 Running unit tests ($TASK_CMD test)..."
  "$TASK_CMD" test
  
  step "3/4 Running race detector tests ($TASK_CMD test-race)..."
  "$TASK_CMD" test-race
  
  step "4/4 Running database integration tests ($TASK_CMD test-integration)..."
  "$TASK_CMD" test-integration
  
  ok "All verification suites passed successfully."
else
  warn "Skipping pre-release test suites (--skip-checks flag was set)."
fi

# Confirmation
echo
info "Release preflight verification passed:"
echo -e "  • Version tag  : ${CLR_BOLD}${TAG}${CLR_RESET}"
echo -e "  • Target branch: ${CLR_BOLD}${CURRENT_BRANCH}${CLR_RESET}"
echo -e "  • Commit SHA   : ${CLR_BOLD}${LOCAL_SHA:0:7}${CLR_RESET} ($(git log -1 --pretty=%s))"
echo

read -rp "Create and push annotated tag '${TAG}' to trigger the release pipeline? [y/N] " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || err "Release aborted."

# Execution
git tag -a "$TAG" -m "Release $TAG"
git push origin "$TAG"

echo
ok "Successfully created and pushed tag '${CLR_BOLD}${TAG}${CLR_RESET}'."
info "GitHub Actions release pipeline is running: ${CLR_BOLD}https://github.com/ju4n97/hclapi/actions${CLR_RESET}"