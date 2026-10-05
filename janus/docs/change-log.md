08:29PM - Feb-27-2026
- Started working on marking off tasks in linear for the MVP doc from my chat with CODEX
- Completed [[Implement Resource Estimation Engine]](https://linear.app/stevenclaw/issue/GS-11/implement-resource-estimation-engine)
- Completed [Attach Resource Profile to BuildJob](https://linear.app/stevenclaw/issue/GS-12/attach-resource-profile-to-buildjob)

08:37PM - Feb-27-2026
- Completed [Implement Deployment API Endpoint](https://linear.app/stevenclaw/issue/GS-13/implement-deployment-api-endpoint)
- Completed [Persist Deployment Status](https://linear.app/stevenclaw/issue/GS-15/persist-deployment-status)
- Completed [Implement Temporary Domain Generator](https://linear.app/stevenclaw/issue/GS-16/implement-temporary-domain-generator) 
- Completed [Create Domain Binding Persistence](https://linear.app/stevenclaw/issue/GS-18/create-domain-binding-persistence)

08:46PM - Feb-27-2026
- Completed [Write Deployment Documentation](https://linear.app/stevenclaw/issue/GS-28/write-deployment-documentation)
- Started working on [Implement Git Provider Abstraction Layer](https://linear.app/stevenclaw/issue/GS-6/implement-git-provider-abstraction-layer)

08:46 - Feb-27-2026
- Completed  [Implement Git Provider Abstraction Layer](https://linear.app/stevenclaw/issue/GS-6/implement-git-provider-abstraction-layer)
- Started working on [Issue 008 — Implement Executor Service Base](https://linear.app/stevenclaw/issue/GS-8/implement-executor-service-base)


09:04PM - Feb-27-2026
- Completed [Implement Executor Service Base](https://linear.app/stevenclaw/issue/GS-8/implement-executor-service-base)
- Started Working on [010 — Implement Docker Build Execution](https://linear.app/stevenclaw/issue/GS-10/implement-docker-build-execution)

09:14PM - Feb-27-2026
- Continued working on [010 — Implement Docker Build Execution](https://linear.app/stevenclaw/issue/GS-10/implement-docker-build-execution)
- I noticed that the dockerfile.go has formatting issues so I am investigation
- AFK 9:17PM
- Back 9:18PM
- Completed [010 — Implement Docker Build Execution](https://linear.app/stevenclaw/issue/GS-10/implement-docker-build-execution)

09:22PM - Feb-27-2026
- Long Break (20 Mins)

09:44PM - Feb-27-2026
-  Started working on [Issue 011 — Persist Build Logs and Status](https://linear.app/stevenclaw/issue/GS-31/persist-build-logs-and-status)
-  Completed [Issue 011 — Persist Build Logs and Status](https://linear.app/stevenclaw/issue/GS-31/persist-build-logs-and-status)
-  Started [Issue 015 — Implement Container Runtime Launch](https://linear.app/stevenclaw/issue/GS-14/implement-container-runtime-launch)

09:52PM - Feb-27-2026
- Completed [Issue 015 — Implement Container Runtime Launch](https://linear.app/stevenclaw/issue/GS-14/implement-container-runtime-launch)
- Started Working on [implement: Issue 018 — Implement Reverse Proxy Routing](https://linear.app/stevenclaw/issue/GS-17/implement-reverse-proxy-routing)

10:02PM - Feb-27-2026
- Completed [implement: Issue 018 — Implement Reverse Proxy Routing](https://linear.app/stevenclaw/issue/GS-17/implement-reverse-proxy-routing)
- Started [Implement Issue 020 — Initialize Next.js Frontend](https://linear.app/stevenclaw/issue/GS-19/initialize-nextjs-frontend)
- Needed to install node so I did that

10:11PM - Feb-27-2026
- Continued working on [Implement Issue 020 — Initialize Next.js Frontend](https://linear.app/stevenclaw/issue/GS-19/initialize-nextjs-frontend)
- Completed [Implement Issue 020 — Initialize Next.js Frontend](https://linear.app/stevenclaw/issue/GS-19/initialize-nextjs-frontend)
- Started [Issue 021 — Implement Project Creation UI](https://linear.app/stevenclaw/issue/GS-20/implement-project-creation-ui)

10:21PM - Feb-27-2026
- Continued working on [Issue 021 — Implement Project Creation UI](https://linear.app/stevenclaw/issue/GS-20/implement-project-creation-ui)
- Completed [Issue 021 — Implement Project Creation UI](https://linear.app/stevenclaw/issue/GS-20/implement-project-creation-ui)
- Started [Issue 022 — Implement Repo Import UI](https://linear.app/stevenclaw/issue/GS-21/implement-repo-import-ui)

10:30PM - Feb-27-2026
- Completed [Issue 022 — Implement Repo Import UI](https://linear.app/stevenclaw/issue/GS-21/implement-repo-import-ui)
- Completed [Issue 023 — Implement Build Status UI](https://linear.app/stevenclaw/issue/GS-22/implement-build-status-ui)
- Completed [Issue 024 — Implement Deployment UI](https://linear.app/stevenclaw/issue/GS-23/implement-deployment-ui)
- Completed [Issue 025 — Implement Domain Display UI](https://linear.app/stevenclaw/issue/GS-24/implement-domain-display-ui)
- Completed [Issue 026 — Connect Control Plane to Executor](https://linear.app/stevenclaw/issue/GS-25/connect-control-plane-to-executor)
- Completed [Issue 027 — End-to-End Pipeline Test with playwright](https://linear.app/stevenclaw/issue/GS-26/end-to-end-pipeline-test-with-playwright)

10:38PM - Feb-27-2026
- Long Break (20 Mins)
- Converted Makefile into a Powershell file.

10:58PM - Feb-27-2026
- Going to continue working on testing the Janus.ps1
- Got an error when I started the backend when the database is not up and running, going to alter the janus.ps1 to account for this when it gets the error and calls the docker commands to make sure the containers are up, making sure the containers are built first. 

11:07PM - Feb-27-2026
- Continued working on getting the test file working to run the backend to test the frontend
- Docker Composing is failing so I am investigation
- I found that when I archieved the OG NextJS repo, I deleted the docker-compose file
  - I found the old one and plopped it back into the repo
- The Janus frontend does not have the ability to log in so I dont get the janus_access cookie...generating that now

11:24PM - Feb-27-2026
- The frontend can now log me in and log me out, in testing of the frontend I can create a project, but when I gets built it errors with Error, no Dockerfile found and no supported project type detected.
- I also noticed that we dont have the cli running in its own container, so I am going to make sure that gets done.
- Made sure that worked
- Testing the frontend to the backend now

11:43PM - Feb-27-2026
- Git is apprently not installed on the backend image, making sure it does
- its there now

11:52PM - Feb-27-2026
- I need to research and make sure that my container works with docker in docker (DiD)
  -  https://www.docker.com/resources/docker-in-docker-containerized-ci-workflows-dockercon-2023/

12:00PM - Feb-27-2026
- Planning out the infrustucture for Docker in Docker
- I need this because that functionality is for when the build process kicks off
- When that happens the intent is to spin up a docker container that is inside the docker container that is running the api, this is needed to isolate it because it is running any code that can live on any git repo, very serious and potentially dangourous stuff that we need to get right.
- The flow in my head is that when they hit import repo and the build starts, it then analyses the repo to see what kind of container we need to spin up, for now for the MVP i think we need a generic image that works for most deployments, and then make sure that container has git installed, then pull the repo supplied to us from when they imported it. Once that is complete the status is relayed back to the API and then back to the frontend. 
- The talk from DockerCon about the Docker in Docker Contanerized CI Workflows speaks to me as the perfect direction to reasearch further for the capabilties it is highlighting, as opposed to running Firecracker(MicroVM) I am not sure which one would be more complicated.
- A pratical MVP we are going to go forward with after some research is as follows
  - First a seperate worker (non-negotiable for safety)
    - API only queues jobs + streams status
    - Worker si the only thing allowed to run build sandboxes
  - Second, Per-job sandbox container (DinD-capable)
    - launch a "system container" per job(Sysbox-Style)
    - Inside
      - git clone the repo
      - detect build type
      - run build pipeline(which may involve spinning nested containers)
  - Explicitly avoid the two common footguns
    - No host docker socket mount(Docker out of Docker), because it exposes/control host containers
    - avoid priviliged DinD for untrusted builds (if you can avoid it, it should only ever happen on isolated worker nodes, not on the API host)
- Report status back
  - worker updates DB Status + streams logs
  - API Relays to frontend

12:20PM - Feb-27-2026
- Continued research in my conversatin with codex on how to plan this out

12:31PM - Feb-27-2026
- The GS-26 MVP plan separates the API and worker so the API acts only as a control plane while the janus-worker handles all cloning, building, and deployment using isolated per-job build sandboxes and a worker-local container daemon. Jobs are queued and leased through Postgres, with the worker executing them, updating logs/status, and ensuring recovery if it crashes. The API never touches the container runtime and only serves requests and status to the frontend, preserving existing polling endpoints. This design improves security and reliability by isolating untrusted repo execution away from the API while keeping the MVP simple by running both services on the same host.

12:38PM - Feb-27-2026
- Implementing the plan

12:46PM - Feb-27-2026
- Still implementing plan

12:56PM - Feb-27-2026
- Still implementing plan
- Codex is running smoke test for end to end testing
  - Auth + session persistence works (signup → subsequent requests)
  - Project creation works
  - Import triggers a build job
  - Worker picks up and completes a build (status transitions + logs)
  - On success, worker can launch runtime via deployments
  - Domain binding is created and discoverable via API
  - The new worker path is now running end-to-end in smoke tests

01:14AM - Feb-28-2026

Implemented GS-26 end-to-end in code with working smoke validation.

09:31AM - March-29-2026
- Refactored the codebase to use the Ports and Adapters infrastructure design

09:57AM - Match-29-2026
- Moved Janus background build and deployment execution behind application-owned ports so the supervisor is now a pure scheduler and Postgres only owns queue/lease persistence.


