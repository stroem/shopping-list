# infra/

Infrastructure-as-code for deploying the backend as an **AWS Lambda behind an
API Gateway HTTP API**, connected to **Neon Postgres** (scale-to-zero).

This directory is a placeholder. The actual IaC (SAM or Terraform) lands with
[#4 — Lambda adapter + API Gateway + Neon wiring](https://github.com/stroem/shopping-list/issues/4).
Design constraint: keep running cost ≈ 0 (no always-on compute).
