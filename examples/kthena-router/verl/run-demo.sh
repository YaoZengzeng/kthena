#!/usr/bin/env bash
# run-demo.sh -- verl + Kthena router demo on a node with 2 x ~20GB GPUs.
#
# Runs GRPO on GSM8K with Qwen3-0.6B in a single pod, with the two vLLM rollout
# replicas fronted by a standalone kthena-router binary. The router reads its
# ModelServer and ModelRoute manifests from a directory
# (--resource-source=file), so it needs no controller and no CRDs in the
# cluster: the whole demo is one pod plus a Hydra override.
#
# Usage:
#   bash run-demo.sh                # rollouts routed by the Kthena router
#   bash run-demo.sh native         # baseline, verl's built-in round-robin
#   bash run-demo.sh logs           # copy the router and training logs out
#   bash run-demo.sh teardown       # delete the pod
#
# Env overrides (all optional):
#   POD            pod name                   (default: verl-kthena-demo)
#   VERL_COMMIT    verl commit to install
#   MODEL          HuggingFace model id       (default: Qwen/Qwen3-0.6B)
#   STEPS          training steps             (default: 2)
#   N              GRPO rollout group size    (default: 4)
#   GPU_MEM_UTIL   vLLM memory fraction       (default: 0.4, lower it on OOM)
#   SKIP_SETUP=1   reuse an already prepared pod
set -euo pipefail

MODE="${1:-kthena}"
POD="${POD:-verl-kthena-demo}"
VERL_REPO="${VERL_REPO:-https://github.com/volcengine/verl.git}"
VERL_COMMIT="${VERL_COMMIT:-b5021d3da11fe78e64e9dbfc175641e7a0a874fd}"
MODEL="${MODEL:-Qwen/Qwen3-0.6B}"
STEPS="${STEPS:-2}"
N="${N:-4}"
GPU_MEM_UTIL="${GPU_MEM_UTIL:-0.4}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
START_TS=$(date +%s)
elapsed() { printf '[%dm%02ds]' $((($(date +%s) - START_TS) / 60)) $((($(date +%s) - START_TS) % 60)); }
in_pod() { kubectl exec -i "$POD" -- bash -c "$1"; }

case "$MODE" in
teardown)
  kubectl delete pod "$POD" --ignore-not-found
  exit 0
  ;;
logs)
  kubectl cp "$POD:/workspace/demo/train.log" ./train.log
  kubectl cp "$POD:/workspace/router/router.log" ./router.log
  kubectl cp "$POD:/workspace/router/resources/rollout.yaml" ./rollout.yaml
  echo ">> wrote train.log, router.log and rollout.yaml to $PWD"
  exit 0
  ;;
kthena)
  ROUTING_OVERRIDES=(
    "+actor_rollout_ref.rollout.agent.agent_loop_manager_class=kthena.verl_integration.agent_loop_manager.KthenaRouterAgentLoopManager"
    "+actor_rollout_ref.rollout.custom.kthena_router_binary=/workspace/kthena/kthena-router"
    "+actor_rollout_ref.rollout.custom.kthena_router_config=/workspace/kthena/router-config.yaml"
    "+actor_rollout_ref.rollout.custom.kthena_router_work_dir=/workspace/router"
  )
  ;;
native)
  ROUTING_OVERRIDES=()
  ;;
*)
  echo "ERROR: mode must be 'kthena', 'native', 'logs' or 'teardown' (got '$MODE')" >&2
  exit 1
  ;;
esac

# --- 1. start the pod ---------------------------------------------------------
if ! kubectl get pod "$POD" >/dev/null 2>&1; then
  echo "$(elapsed) >> creating pod '$POD' (the first image pull is ~12GB and one-time)"
  sed "s/name: verl-kthena-demo/name: $POD/" "$HERE/pod.yaml" | kubectl apply -f -
fi
kubectl wait --for=condition=Ready "pod/$POD" --timeout=20m

# --- 2. ship the router binary and the integration into the pod ---------------
if [[ "${SKIP_SETUP:-0}" != "1" ]]; then
  echo "$(elapsed) >> building bin/kthena-router and copying it into the pod"
  (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/kthena-router ./cmd/kthena-router)

  in_pod 'mkdir -p /workspace/kthena /workspace/demo'
  tar czf - -C "$REPO_ROOT/bin" kthena-router | in_pod 'tar xzf - -C /workspace/kthena'
  tar czf - -C "$REPO_ROOT/python" --exclude=__pycache__ kthena | in_pod 'tar xzf - -C /workspace/kthena'
  tar czf - -C "$HERE" router-config.yaml | in_pod 'tar xzf - -C /workspace/kthena'

  # --- 3. one-time setup inside the pod ---------------------------------------
  echo "$(elapsed) >> preparing verl, the integration, GSM8K and $MODEL (one-time)"
  in_pod "
set -euo pipefail
exec > >(tee -a /workspace/demo/setup.log) 2>&1
chmod +x /workspace/kthena/kthena-router

# verl itself is not in the environment image; install it from source.
if [[ ! -d /workspace/demo/verl/.git ]]; then
  mkdir -p /workspace/demo/verl && cd /workspace/demo/verl && git init -q
  git remote add origin '$VERL_REPO'
  git fetch --depth 1 -q origin '$VERL_COMMIT'
  git checkout -q FETCH_HEAD
fi
pip install --no-deps -q -e /workspace/demo/verl

# Make the Kthena integration importable from every process in the pod,
# including the Ray actors that verl spawns.
python3 -c \"
import site
open(site.getsitepackages()[0] + '/kthena-verl-integration.pth', 'w').write('/workspace/kthena')
\"
python3 -c 'import verl, kthena.verl_integration'

python3 /workspace/demo/verl/examples/data_preprocess/gsm8k.py --local_save_dir /workspace/demo/data/gsm8k &
python3 -c \"
from huggingface_hub import snapshot_download
snapshot_download('$MODEL', local_dir='/workspace/demo/models/$MODEL')
\" &
wait
"
fi

# --- 4. train -----------------------------------------------------------------
echo "$(elapsed) >> training (mode=$MODE, steps=$STEPS, n=$N, gpu_mem_util=$GPU_MEM_UTIL)"
in_pod "
set -euxo pipefail
exec > /workspace/demo/train.log 2>&1
cd /workspace/demo/verl

python3 -m verl.trainer.main_ppo \
  algorithm.adv_estimator=grpo \
  data.train_files=/workspace/demo/data/gsm8k/train.parquet \
  data.val_files=/workspace/demo/data/gsm8k/test.parquet \
  data.train_batch_size=8 \
  data.max_prompt_length=512 \
  data.max_response_length=128 \
  data.filter_overlong_prompts=True \
  actor_rollout_ref.model.path=/workspace/demo/models/$MODEL \
  actor_rollout_ref.actor.ppo_mini_batch_size=8 \
  actor_rollout_ref.actor.ppo_micro_batch_size_per_gpu=4 \
  actor_rollout_ref.actor.use_kl_loss=True \
  actor_rollout_ref.rollout.log_prob_micro_batch_size_per_gpu=8 \
  actor_rollout_ref.rollout.name=vllm \
  actor_rollout_ref.rollout.mode=async \
  actor_rollout_ref.rollout.tensor_model_parallel_size=1 \
  actor_rollout_ref.rollout.gpu_memory_utilization=$GPU_MEM_UTIL \
  actor_rollout_ref.rollout.n=$N \
  actor_rollout_ref.rollout.enforce_eager=True \
  actor_rollout_ref.rollout.max_model_len=1024 \
  actor_rollout_ref.rollout.max_num_batched_tokens=1024 \
  actor_rollout_ref.rollout.max_num_seqs=32 \
  actor_rollout_ref.rollout.disable_log_stats=False \
  actor_rollout_ref.ref.log_prob_micro_batch_size_per_gpu=8 \
  actor_rollout_ref.ref.fsdp_config.param_offload=True \
  trainer.logger='[\"console\"]' \
  trainer.n_gpus_per_node=2 \
  trainer.nnodes=1 \
  trainer.save_freq=-1 \
  trainer.test_freq=-1 \
  trainer.val_before_train=False \
  trainer.total_training_steps=$STEPS \
  trainer.default_local_dir=/workspace/demo/checkpoints \
  hydra.run.dir=/workspace/demo/hydra \
  ray_kwargs.ray_init.num_cpus=32 \
  ${ROUTING_OVERRIDES[*]}
"

echo "$(elapsed) >> done."
if [[ "$MODE" == "kthena" ]]; then
  printf '   rollouts scheduled by the router: '
  in_pod 'grep -c "POST \"/v1/schedule\"" /workspace/router/router.log'
fi
echo "   copy the logs out with: bash $0 logs"
echo "   tear down with:         bash $0 teardown"
