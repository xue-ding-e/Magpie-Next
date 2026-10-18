#!/usr/bin/env bash
# build.sh <target> <out.zip>: pf-serve for one of darwin-arm64,
# darwin-amd64, linux-amd64, linux-arm64, windows-amd64, zipped.
#
# privacy-filter.cpp and ggml are taken at the commits pinned here, with
# patches/ applied. A target with a GPU through Vulkan (linux-amd64,
# windows-amd64) gets two programs: pf-serve-vulkan, linked to the Vulkan
# loader the GPU's driver installs, and pf-serve, the CPU's alone, for a
# computer with no loader, where pf-serve-vulkan can't start at all.
# darwin-arm64's pf-serve uses Metal.
set -euo pipefail

PF_REPO=https://github.com/localai-org/privacy-filter.cpp
PF_COMMIT=735a6c28607ee82afc3a670383f41b55266a3b9a
VULKAN_SDK_TAG=1.4.309
SPIRV_TAG=vulkan-sdk-1.4.309.0

target=$1
out=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
here=$(cd "$(dirname "$0")" && pwd)
work=${PF_WORK:-$here/.work}
src=$work/privacy-filter.cpp
mkdir -p "$work"

if [ ! -d "$src/.git" ]; then
  git init -q "$src"
  git -C "$src" remote add origin "$PF_REPO"
fi
if [ "$(git -C "$src" rev-parse -q --verify HEAD || true)" != "$PF_COMMIT" ]; then
  git -C "$src" fetch -q --depth 1 origin "$PF_COMMIT"
  git -C "$src" checkout -q --force FETCH_HEAD
fi
git -C "$src" submodule update -q --init --depth 1 ggml
git -C "$src" checkout -q -- .
git -C "$src/ggml" checkout -q -- .
for p in "$here"/patches/*.patch; do
  git -C "$src" apply "$p"
done

# Vulkan's headers, SPIR-V's and the loader's exports, for the targets
# that build pf-serve-vulkan
vk=$work/vulkan
fetch_vulkan() {
  [ -f "$vk/.done-$VULKAN_SDK_TAG" ] && return
  rm -rf "$vk" && mkdir -p "$vk/include" "$vk/lib"
  curl -fsSL "https://github.com/KhronosGroup/Vulkan-Headers/archive/refs/tags/v$VULKAN_SDK_TAG.tar.gz" | tar -xz -C "$vk"
  curl -fsSL "https://github.com/KhronosGroup/SPIRV-Headers/archive/refs/tags/$SPIRV_TAG.tar.gz" | tar -xz -C "$vk"
  cp -R "$vk/Vulkan-Headers-$VULKAN_SDK_TAG/include/"* "$vk/include/"
  cp -R "$vk/SPIRV-Headers-$SPIRV_TAG/include/"* "$vk/include/"
  curl -fsSL -o "$vk/vulkan-1.def" "https://raw.githubusercontent.com/KhronosGroup/Vulkan-Loader/v$VULKAN_SDK_TAG/loader/vulkan-1.def"
  touch "$vk/.done-$VULKAN_SDK_TAG"
}

jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)
stage=$work/stage-$target
rm -rf "$stage" && mkdir -p "$stage"

# build <name> <cmake args…>: pf-serve built with those, copied to the
# stage as name
build() {
  local name=$1
  shift
  local b=$work/build-$target-$name
  cmake -S "$here" -B "$b" -G Ninja -DCMAKE_BUILD_TYPE=Release -DPF_SRC="$src" \
    -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF "$@"
  cmake --build "$b" -j "$jobs" --target pf-serve
  local exe=pf-serve
  case $target in windows-*) exe=pf-serve.exe ;; esac
  cp "$b/$exe" "$stage/$name${exe#pf-serve}"
}

x86=(-DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_BMI2=ON)
case $target in
  darwin-arm64)
    build pf-serve -DCMAKE_OSX_ARCHITECTURES=arm64 -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 \
      -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DGGML_BLAS=OFF
    ;;
  darwin-amd64)
    build pf-serve -DCMAKE_SYSTEM_NAME=Darwin -DCMAKE_SYSTEM_PROCESSOR=x86_64 -DCMAKE_OSX_ARCHITECTURES=x86_64 \
      -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 -DGGML_METAL=OFF -DGGML_BLAS=OFF "${x86[@]}"
    ;;
  linux-amd64)
    fetch_vulkan
    static=(-DCMAKE_EXE_LINKER_FLAGS="-static-libgcc -static-libstdc++")
    build pf-serve "${static[@]}" "${x86[@]}"
    build pf-serve-vulkan "${static[@]}" "${x86[@]}" -DPF_VULKAN=ON -DVulkan_INCLUDE_DIR="$vk/include"
    ;;
  linux-arm64)
    build pf-serve -DCMAKE_EXE_LINKER_FLAGS="-static-libgcc -static-libstdc++"
    ;;
  windows-amd64)
    fetch_vulkan
    x86_64-w64-mingw32-dlltool -d "$vk/vulkan-1.def" -l "$vk/lib/libvulkan-1.a"
    win=(-DCMAKE_TOOLCHAIN_FILE="$here/mingw.cmake" -DCMAKE_C_FLAGS=-D_WIN32_WINNT=0x0A00 -DCMAKE_CXX_FLAGS=-D_WIN32_WINNT=0x0A00)
    build pf-serve "${win[@]}" "${x86[@]}"
    build pf-serve-vulkan "${win[@]}" "${x86[@]}" -DPF_VULKAN=ON -DVulkan_INCLUDE_DIR="$vk/include" \
      -DVulkan_LIBRARY="$vk/lib/libvulkan-1.a" -DVulkan_GLSLC_EXECUTABLE="$(command -v glslc)"
    ;;
  *)
    echo "unknown target $target" >&2
    exit 2
    ;;
esac

cp "$src/LICENSE" "$stage/LICENSE-privacy-filter.cpp"
cp "$src/ggml/LICENSE" "$stage/LICENSE-ggml"
rm -f "$out"
(cd "$stage" && zip -q -X -r "$out" .)
ls -l "$out"
