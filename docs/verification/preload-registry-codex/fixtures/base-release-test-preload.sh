# Fixture: the inline "Pre-load e2e test images" step of .github/workflows/release-test.yml
# as of base commit 73e05db4213950a5dd1f230279e9344a4ffe8435 (extracted verbatim,
# YAML indentation stripped). NOTE: plain `docker pull` with NO retry.
set -euo pipefail
image_archive=$(mktemp "${RUNNER_TEMP}/kind-image.XXXXXX")
trap 'rm -f "${image_archive}"' EXIT
E2E_IMAGES=(
  "registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest"
  "registry-cn-hangzhou.ack.aliyuncs.com/dev/patio-runtime:v0.2.0"
)
for image in "${E2E_IMAGES[@]}"; do
  echo "Pulling image: ${image}"
  docker pull "${image}"
  echo "Loading image into Kind: ${image}"
  docker image save --platform "${KIND_IMAGE_PLATFORM}" --output "${image_archive}" "${image}"
  kind load image-archive "${image_archive}" --name rbgs-e2e
done
echo "All e2e test images loaded successfully"
