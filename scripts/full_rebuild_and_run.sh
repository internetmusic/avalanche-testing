set -euo pipefail
script_dirpath="$(cd "$(dirname "${0}")" && pwd)"

test_names="${1:-}"

bash "${script_dirpath}/rebuild_controller_image.sh"
bash "${script_dirpath}/rebuild_initializer_binary.sh"
bash "${script_dirpath}/run.sh" "${test_names}"
