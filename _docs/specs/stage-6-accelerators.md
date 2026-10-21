# Stage 6: accelerators

Goal: one Teak kernel runs on a GPU through the LLVM targets, under the rule that nothing in earlier stages had to change to allow it.

## Task 6.1: Kernel-safe subset

**Description:** A design note and a checker mode that identifies functions in the kernel-safe subset: no allocation, no tasks, no trait-as-type dispatch, no unbounded recursion.

**Acceptance criteria:**
- [ ] `../design/08-kernels.md` defines the subset
- [ ] A function outside the subset that is marked as a kernel reports why

**Verification:**
- [ ] Tests pass: `go test ./compiler/types/`

**Dependencies:** Stage 5
**Files:** `../design/08-kernels.md`, `compiler/types/kernel.go`
**Scope:** M

## Task 6.2: `Device<T>` and launch

**Description:** A `Device<T>` buffer whose moves are host-to-device copies, freed by `Drop`; a kernel launch as a function call with a grid argument; emission for NVPTX or AMDGPU.

**Acceptance criteria:**
- [ ] A vector-add kernel compiles to device code
- [ ] A device buffer leaked at scope end is impossible by construction

**Verification:**
- [ ] Tests pass on a machine with a supported GPU; skipped elsewhere

**Dependencies:** 6.1
**Files:** `std/device/`, `compiler/emit/llvm/gpu.go`
**Scope:** L

## Task 6.3: One example kernel

**Description:** A twenty-first example, a matrix multiply from example 16's types, running on a GPU with its CPU result as the check.

**Acceptance criteria:**
- [ ] `examples/21-gpu-matmul.teak` runs and matches the CPU result

**Verification:**
- [ ] Manual check on GPU hardware

**Dependencies:** 6.2
**Files:** `examples/21-gpu-matmul.teak`
**Scope:** M

## Checkpoint: stage 6

- [ ] Kernel runs; no earlier-stage change was needed
- [ ] The book chapters this stage owns (see `../book/README.md`) are written and their status lines updated
- [ ] Reviewed by the owner
