# Janus Vision

## Why Janus

**Janus** is named after the Roman god of beginnings, gates, transitions, time, and doorways.
The platform should feel like a gateway from source code to production runtime.

---

## Product Inspiration

### Vercel / DigitalOcean (Developer Experience)

- Frictionless **new project** creation
- Easy **Git provider** connection
- Fast **repository import**
- **Premade deployment templates** for frameworks

### ngrok (Networking Experience)

- Automatic temporary domain generation for instant deploy previews
  - Example style: `https://xxxx.ngrok.app`
- Bring-your-own-domain support via DNS CNAME to Janus infrastructure

### Encore.dev (Code-First Infrastructure)

From code, Janus should infer/generate backend building blocks:

- Services
- APIs
- Databases
- Cron jobs
- Pub/Sub
- Object storage
- Caching

---

## Core Janus Flow

1. User writes code
2. Code is imported into Janus (Git or template)
3. Janus containerizes app (Docker)
4. Janus runs checks/tests to estimate resource demand
5. User chooses runtime target:
   - Personal servers
   - Janus distributed network
6. Janus deploys and assigns domain:
   - Auto domain by default
   - Optional custom domain via CNAME

---

## Compute Marketplace Direction

Janus distributed network should support one or more economic modes:

1. **Per-minute billing** (cloud-like, inspired by AWS)
2. **Reward model** (monetary or points-based, inspired by Grass-like systems)
3. **Hybrid** (owner chooses billing vs rewards)

Provider nodes can donate or allocate approved resources; consumers run workloads with transparent pricing/credits.

---

## Product Principles

- **Instant first deploy** (minimal setup)
- **Code-first infra generation** (reduce YAML burden)
- **Sane defaults + escape hatches**
- **Transparent resource/cost visibility**
- **Portable workloads** (self-hosted and distributed network)
- **Security-first by default**

---

## v0.1 Target Outcome

A developer can:

- Connect GitHub
- Import a repo
- Build container
- See estimated resource profile
- Deploy to one target (initially self-hosted or a controlled test node)
- Receive auto-generated domain

And optionally configure a custom domain with documented CNAME setup.

# What, Does What, Now What

This document establishes a clear understanding of Janus by defining what it is, what it does, and what actions follow. The goal is to present Janus as a unified repository-to-runtime platform that transforms source code into live, managed applications.

---

# What Is It

Janus is a system that turns code repositories into isolated, live applications that can run, be accessed online, and be managed automatically.

It accepts repositories directly from sources such as GitHub or GitLab and automatically converts each repository into its own sandboxed runtime environment. These environments operate independently, ensuring isolation, security, and reliability. Once created, the runtime is bound to a domain and exposed online, where it can be accessed, managed, and observed.

**Main Idea**

Janus transforms code repositories into isolated, live applications that run online and are automatically managed.

---

# What Does It Do

Janus lets users upload a code repository, automatically turns it into a secure, sandboxed container, assigns it a domain, publishes it online, and provides tools to monitor and manage its runtime, resources, and performance.

It handles the full lifecycle from repository ingestion to live deployment. Janus builds the runtime, verifies its operation, exposes it as a network-accessible service, and provides observability into system performance, networking, caching, and resource usage.

**Main Idea**

Janus converts repositories into secure containers, publishes them online, and provides full runtime management and observability.

---

# What’s Next

Janus enables you to securely configure, automatically deploy, and continuously monitor and scale your applications with minimal manual effort.

Users can configure runtime environments, automate deployment workflows, and use built-in monitoring to observe performance and scale applications as needed. This allows applications to evolve continuously without requiring manual infrastructure management.

**Main Idea**

Janus allows applications to be securely configured, automatically deployed, and continuously monitored and scaled.

---

# Janus — The Repository-to-Runtime Platform

Janus is a platform that transforms code repositories into secure, isolated live applications that can run and be accessed online. It automatically converts repositories into sandboxed containers, assigns them domains, publishes them as network-accessible services, and provides built-in tools to manage, monitor, and control their runtime and performance. By handling configuration, deployment, observability, and scaling, Janus enables applications to move directly from source code to fully managed, production-ready systems with minimal infrastructure overhead.

---

# Core Model

Repository → Container → Service → Managed Runtime → Observable System

---

# Core Principles

- Repository-first architecture  
- Automatic containerization and isolation  
- Built-in deployment and publishing  
- Integrated runtime management  
- Continuous observability and scaling  
- Zero manual infrastructure requirement  

---

# Outcome

Janus eliminates the gap between writing code and running production systems by transforming repositories into fully operational, managed services automatically.
