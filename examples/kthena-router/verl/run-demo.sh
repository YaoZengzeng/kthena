#!/usr/bin/env bash
# run-demo.sh -- one-shot verl + Kthena router demo on 2 x ~20GB GPUs.
#
# Runs GRPO on GSM8K with Qwen3-0.6B in a single verl container, with the two
# vLLM rollout replicas fronted by a standalone kthena-router binary. No
# Kubernetes, no Ray cluster manifest: the router reads its ModelServer and
# ModelRoute manifests from a directory (--resource-source=file), so the whole
# demo is `docker run` plus a Hydra override.
#
# Usage:
#   bash run-demo.sh                # rollouts routed by the Kthena router
#   bash run-demo.sh native         # baseline, verl's built-in round-robin
#   bash run-demo.sh teardown       # remove the container
#
# Env overrides (all optional):
#   IMAGE          verl environment image     (default: verlai/verl:vllm024.dev2)
#   CONTAINER      container name             (default: kthena-verl-demo)
#   DEMO_DIR       host cache dir             (default: /root/kthena-verl-demo)
#   VERL_REPO      verl git remote            (default: https://github.com/volcengine/verl.git)
#   VERL_COMMIT    verl commit to install
#   MODEL          HuggingFace model id       (default: Qwen/Qwen3-0.6B)
#   STEPS          training steps             (default: 3)
#   N              GRPO rollout group size    (default: 8)
#   GPU_MEM_UTIL   vLLM memory fraction       (default: 0.5, lower it on OOM)
#   SKIP_SETUP=1   reuse an already prepared container
set -euo pipefail

MODE="${1:-kthena}"
IMAGE="${IMAGE:-verlai/verl:vllm024.dev2}"
CONTAINER="${CONTAINER:-kthena-verl-demo}"
DEMO_DIR="${DEMO_DIR:-/root/kthena-verl-demo}"
VERL_REPO="${VERL_REPO:-https://github.com/volcengine/verl.git}"
VERL_COMMIT="${VERL_COMMIT:-b5021d3da11fe78e64e9dbfc175641e7a0a874fd}"
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"
STEPS="${STEPS:-3}"
N="${N:-8}"
GPU_MEM_UTIL="${GPU_MEM_UTIL:-0.5}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
START_TS=$(date +%s)
elapsed() { printf '[%dm%02ds]' $(((($(date +%s) - START_TS)) / 60)) $((($(date +%s) - START_TS) % 60)); }

if [[ "$MODE" == "teardown" ]]; then
  echo ">> removing container '$CONTAINER'"
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  echo ">> cached models, data and logs are kept in $DEMO_DIR"
  exit 0
fi

case "$MODE" in
kthena)
  ROUTING_OVERRIDES=(
    "+actor_rollout_ref.rollout.agent.agent_loop_manager_class=kthena.verl_integration.agent_loop_manager.KthenaRouterAgentLoopManager"
    "+actor_rollout_ref.rollout.custom.kthena_router_binary=/workspace/kthena/bin/kthena-router"
    "+actor_rollout_ref.rollout.custom.kthena_router_config=/workspace/kthena/examples/kthena-router/verl/router-config.yaml"
    "+actor_rollout_ref.rollout.custom.kthena_router_work_dir=/workspace/demo/router"
  )
  EXPERIMENT_NAME="kthena_router_2gpu"
  ;;
native)
  ROUTING_OVERRIDES=()
  EXPERIMENT_NAME="native_2gpu"
  ;;
*)
  echo "ERROR: mode must be 'kthena', 'native' or 'teardown' (got '$MODE')" >&2
  exit 1
  ;;
esac

# --- 1. build the router binary ----------------------------------------------
if [[ "$MODE" == "kthena" && ! -x "$REPO_ROOT/bin/kthena-router" ]]; then
  echo "$(elapsed) >> building bin/kthena-router"
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o bin/kthena-router ./cmd/kthena-router)
fi

# --- 2. start the container ---------------------------------------------------
mkdir -p "$DEMO_DIR"
if ! docker inspect "$CONTAINER" >/dev/null 2>&1; then
  echo "$(elapsed) >> starting container '$CONTAINER' from $IMAGE (first image pull is large and one-time)"
  docker run -d --name "$CONTAINER" \
    --gpus all --shm-size=16g --ipc=host --ulimit memlock=-1 --ulimit stack=67108864 \
    -v "$REPO_ROOT:/workspace/kthena:ro" \
    -v "$DEMO_DIR:/workspace/demo" \
    -e HOME=/workspace/demo \
    -e HF_HOME=/workspace/demo/hf \
    -e HF_HUB_ENABLE_HF_TRANSFER=0 \
    -e WANDB_MODE=offline \
    -e VERL_LOGGING_LEVEL=INFO \
    -e TOKENIZERS_PARALLELISM=false \
    "$IMAGE" sleep infinity >/dev/null
elif [[ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER")" != "true" ]]; then
  echo "$(elapsed) >> restarting container '$CONTAINER'"
  docker start "$CONTAINER" >/dev/null
fi

# --- 3. one-time setup inside the container ----------------------------------
if [[ "${SKIP_SETUP:-0}" != "1" ]]; then
  echo "$(elapsed) >> preparing verl, the integration, GSM8K and $MODEL (one-time, cached in $DEMO_DIR)"
  docker exec -i \
    -e VERL_REPO="$VERL_REPO" -e VERL_COMMIT="$VERL_COMMIT" -e MODEL="$MODEL" \
    "$CONTAINER" bash -s <<'SETUP'
set -euo pipefail
exec > >(tee -a /workspace/demo/setup.log) 2>&1

if [[ -f /workspace/demo/.setup-done ]]; then
  echo "setup already done, skipping"
  exit 0
fi

# verl itself is not in the environment image; install it from source.
if [[ ! -d /workspace/demo/verl/.git ]]; then
  git clone "$VERL_REPO" /workspace/demo/verl
fi
git -C /workspace/demo/verl fetch --quiet origin "$VERL_COMMIT" || true
git -C /workspace/demo/verl checkout --quiet "$VERL_COMMIT"
pip install --no-deps -e /workspace/demo/verl

# Make the Kthena integration importable from every process in the container,
# including the Ray actors that verl spawns.
python3 - <<'PY'
import site
with open(f"{site.getsitepackages()[0]}/kthena-verl-integration.pth", "w") as f:
    f.write("/workspace/kthena/python\n")
PY
python3 -c "import kthena.verl_integration"

python3 /workspace/demo/verl/examples/data_preprocess/gsm8k.py --local_save_dir /workspace/demo/data/gsm8k

python3 - <<PY
from huggingface_hub import snapshot_download

snapshot_download("$MODEL", local_dir="/workspace/demo/models/$MODEL")
PY

touch /workspace/demo/.setup-done
echo "setup complete"
SETUP
fi

# --- 4. train -----------------------------------------------------------------
echo "$(elapsed) >> training (mode=$MODE, steps=$STEPS, n=$N, gpu_mem_util=$GPU_MEM_UTIL)"
docker exec -i \
  -e STEPS="$STEPS" -e N="$N" -e GPU_MEM_UTIL="$GPU_MEM_UTIL" -e MODEL="$MODEL" \
  -e EXPERIMENT_NAME="$EXPERIMENT_NAME" \
  "$CONTAINER" bash -s -- "${ROUTING_OVERRIDES[@]}" <<'TRAIN'
set -euxo pipefail

NGPUS_PER_NODE=2 \
TRAIN_BATCH_SIZE=32 \
PPO_MINI_BATCH_SIZE=16 \
MODEL_PATH="/workspace/demo/models/$MODEL" \
TRAIN_FILE=/workspace/demo/data/gsm8k/train.parquet \
TEST_FILE=/workspace/demo/data/gsm8k/test.parquet \
MAX_PROMPT_LENGTH=512 \
MAX_RESPONSE_LENGTH=512 \
SAVE_FREQ=-1 \
PROJECT_NAME=kthena_verl_demo \
  bash /workspace/demo/verl/examples/grpo_trainer/run_qwen3_4b_fsdp.sh \
  actor_rollout_ref.rollout.tensor_model_parallel_size=1 \
  actor_rollout_ref.rollout.n="$N" \
  actor_rollout_ref.rollout.gpu_memory_utilization="$GPU_MEM_UTIL" \
  actor_rollout_ref.rollout.enforce_eager=True \
  actor_rollout_ref.rollout.max_model_len=2048 \
  actor_rollout_ref.rollout.max_num_batched_tokens=2048 \
  actor_rollout_ref.rollout.max_num_seqs=64 \
  actor_rollout_ref.rollout.disable_log_stats=False \
  trainer.logger='["console"]' \
  trainer.total_training_steps="$STEPS" \
  trainer.val_before_train=False \
  trainer.test_freq=-1 \
  trainer.default_local_dir=/workspace/demo/checkpoints \
  hydra.run.dir=/workspace/demo/hydra \
  "$@"
TRAIN

echo "$(elapsed) >> done."
if [[ "$MODE" == "kthena" ]]; then
  echo "   router log:     $DEMO_DIR/router/router.log"
  echo "   router manifests: $DEMO_DIR/router/resources/rollout.yaml"
fi
echo "   tear down with: bash $0 teardown"
