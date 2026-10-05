//! Local deterministic verification runner for the Rust control-plane core.

use std::env;

use janus_core::{
    AssertionDetails, AssertionWriter, BuildState, Command, CommandId, DeploymentState, EntityId,
    Operation, OperationStatus, Simulator, TenantId,
};

fn assertion_output_override() -> Option<String> {
    env::var("JANUS_ASSERTION_EVENTS").ok() // tigerstyle: allow-direct-env — local verification process configuration
}

fn main() {
    let output = argument_value("--output")
        .or_else(assertion_output_override)
        .unwrap_or_else(|| "artifacts/rust-assertion-events.jsonl".to_owned());
    let mut writer = AssertionWriter::new(&output);
    let mut simulator = Simulator::<32, 64>::new();

    run_build_and_operation_scenario(&mut writer, &mut simulator);
    run_fault_injection_scenario(&mut writer);
    run_deployment_scenario(&mut writer);

    println!("antithesis-local: scenario passed; assertion events written to {output}");
}

fn run_build_and_operation_scenario(
    writer: &mut AssertionWriter,
    simulator: &mut Simulator<32, 64>,
) {
    check(
        writer,
        simulator
            .submit(Command::SubmitBuild {
                command_id: CommandId(1),
                build: EntityId(1),
                tenant: TenantId(1),
            })
            .is_ok(),
        "build command is admitted",
        "rust-local/build#1",
        AssertionDetails {
            operation_id: Some("build-1"),
            operation_kind: Some("build.enqueue"),
            next_status: Some("pending"),
            ..AssertionDetails::default()
        },
    );
    check_step(
        writer,
        simulator,
        "build enters running",
        "rust-local/build#2",
    );
    check_step(
        writer,
        simulator,
        "build reaches succeeded",
        "rust-local/build#3",
    );
    let succeeded =
        simulator.world.build(EntityId(1)).map(|build| build.state) == Some(BuildState::Succeeded);
    check(
        writer,
        succeeded,
        "build lifecycle reaches succeeded",
        "rust-local/build#4",
        AssertionDetails {
            operation_id: Some("build-1"),
            operation_kind: Some("build.lifecycle"),
            previous_status: Some("running"),
            next_status: Some("succeeded"),
            ..AssertionDetails::default()
        },
    );
    check(
        writer,
        simulator.world.validate_invariants().is_ok(),
        "world invariants hold after deterministic scenario",
        "rust-local/invariants#1",
        AssertionDetails::default(),
    );
    run_operation_scenario(writer, simulator);
}

fn run_operation_scenario(writer: &mut AssertionWriter, simulator: &mut Simulator<32, 64>) {
    let operation = Operation::new("op-1", "build.enqueue", "req-1", "tenant-1", 1)
        .unwrap_or_else(|error| fail_fast("operation kind is present", error));
    simulator
        .world
        .insert_operation(EntityId(2), operation.clone())
        .unwrap_or_else(|error| fail_fast("operation entity slot is bounded", error));
    let processing = operation
        .mark_processing(2)
        .unwrap_or_else(|error| fail_fast("pending operation can process", error));
    simulator
        .world
        .replace_operation(EntityId(2), processing)
        .unwrap_or_else(|error| fail_fast("operation transition is monotonic", error));
    check(
        writer,
        simulator
            .world
            .operation(EntityId(2))
            .map(|value| value.status)
            == Some(OperationStatus::Processing),
        "operation status transitions never regress",
        "rust-local/operation#processing",
        AssertionDetails {
            operation_id: Some("op-1"),
            operation_kind: Some("build.enqueue"),
            previous_status: Some("pending"),
            next_status: Some("processing"),
            ..AssertionDetails::default()
        },
    );
    let succeeded = simulator
        .world
        .operation(EntityId(2))
        .unwrap_or_else(|| fail_missing("operation component exists"))
        .clone()
        .complete_success("accepted", 3)
        .unwrap_or_else(|error| fail_fast("processing operation can succeed", error));
    simulator
        .world
        .replace_operation(EntityId(2), succeeded)
        .unwrap_or_else(|error| fail_fast("operation completion is monotonic", error));
    check(
        writer,
        simulator
            .world
            .operation(EntityId(2))
            .map(|value| value.status)
            == Some(OperationStatus::Succeeded),
        "operation status transitions are monotonic",
        "rust-local/operation#1",
        AssertionDetails {
            operation_id: Some("op-1"),
            operation_kind: Some("build.enqueue"),
            previous_status: Some("processing"),
            next_status: Some("succeeded"),
            ..AssertionDetails::default()
        },
    );
}

fn run_fault_injection_scenario(writer: &mut AssertionWriter) {
    run_duplicate_effect_scenario(writer);
    run_dropped_effect_scenario(writer);
}

fn run_duplicate_effect_scenario(writer: &mut AssertionWriter) {
    let mut faulted = Simulator::<32, 64>::new();
    check(
        writer,
        faulted
            .submit(Command::SubmitBuild {
                command_id: CommandId(11),
                build: EntityId(11),
                tenant: TenantId(1),
            })
            .is_ok(),
        "fault-injection build is admitted",
        "rust-local/faults#1",
        AssertionDetails::default(),
    );
    faulted.duplicate_next_effect();
    check_step(
        writer,
        &mut faulted,
        "duplicate completion is injected",
        "rust-local/faults#2",
    );
    check(
        writer,
        faulted.replay().first().is_some_and(|record| {
            record.effects_duplicated == 1
                && record.events_persisted == 2
                && record.effects_dropped == 0
        }),
        "duplicate effects are recorded without losing the event transcript",
        "rust-local/faults#3",
        AssertionDetails::default(),
    );
    check_step(
        writer,
        &mut faulted,
        "duplicate completion remains idempotent",
        "rust-local/faults#4",
    );
    check(
        writer,
        faulted.world.build(EntityId(11)).map(|build| build.state) == Some(BuildState::Succeeded),
        "duplicate completion converges to succeeded",
        "rust-local/faults#5",
        AssertionDetails::default(),
    );
    let replayed = Simulator::<32, 64>::from_replay(faulted.replay());
    check(
        writer,
        replayed
            .as_ref()
            .is_ok_and(|value| value.replay() == faulted.replay()),
        "faulted transcript replays deterministically",
        "rust-local/replay#1",
        AssertionDetails::default(),
    );
}

fn run_dropped_effect_scenario(writer: &mut AssertionWriter) {
    let mut dropped = Simulator::<32, 64>::new();
    check(
        writer,
        dropped
            .submit(Command::SubmitBuild {
                command_id: CommandId(21),
                build: EntityId(21),
                tenant: TenantId(1),
            })
            .is_ok(),
        "dropped-effect build is admitted",
        "rust-local/faults#6",
        AssertionDetails::default(),
    );
    dropped.drop_next_effect();
    check_step(
        writer,
        &mut dropped,
        "next effect is dropped",
        "rust-local/faults#7",
    );
    check(
        writer,
        dropped
            .replay()
            .first()
            .is_some_and(|record| record.effects_dropped == 1 && record.effects_duplicated == 0)
            && dropped.world.build(EntityId(21)).map(|build| build.state)
                == Some(BuildState::Running),
        "dropped effect preserves recoverable running state",
        "rust-local/faults#8",
        AssertionDetails::default(),
    );
}

fn run_deployment_scenario(writer: &mut AssertionWriter) {
    let mut deployment = Simulator::<32, 64>::new();
    check(
        writer,
        deployment
            .submit(Command::SubmitBuild {
                command_id: CommandId(31),
                build: EntityId(31),
                tenant: TenantId(1),
            })
            .is_ok(),
        "deployment build is admitted",
        "rust-local/deployment#1",
        AssertionDetails::default(),
    );
    check_step(
        writer,
        &mut deployment,
        "deployment build starts",
        "rust-local/deployment#2",
    );
    check_step(
        writer,
        &mut deployment,
        "deployment build succeeds",
        "rust-local/deployment#3",
    );
    run_deployment_runtime(writer, &mut deployment);
}

fn run_deployment_runtime(writer: &mut AssertionWriter, deployment: &mut Simulator<32, 64>) {
    check(
        writer,
        deployment
            .submit(Command::CreateDeployment {
                command_id: CommandId(32),
                deployment: EntityId(30),
                tenant: TenantId(1),
                build: EntityId(31),
                target_type: String::default(),
                target_ref: String::default(),
                preferred_runner: String::default(),
                environment: Vec::from([]),
            })
            .is_ok(),
        "deployment is admitted from a succeeded build",
        "rust-local/deployment#4",
        AssertionDetails::default(),
    );
    check_step(
        writer,
        deployment,
        "deployment start is requested",
        "rust-local/deployment#5",
    );
    check_step(
        writer,
        deployment,
        "deployment runtime becomes ready",
        "rust-local/deployment#6",
    );
    check_step(
        writer,
        deployment,
        "deployment runtime readiness is applied",
        "rust-local/deployment#7",
    );
    let deployment_running = deployment
        .world
        .deployment(EntityId(30))
        .map(|value| value.state)
        == Some(DeploymentState::Running);
    check(
        writer,
        deployment_running,
        "running deployments have runtime identity and endpoint",
        "rust-local/deployment#8",
        AssertionDetails {
            deployment_id: Some("dep_30"),
            runtime_id: Some("run_30"),
            endpoint: Some("http://runtime.local"),
            previous_status: Some("starting"),
            next_status: Some("running"),
            ..AssertionDetails::default()
        },
    );
}

fn check_step(
    writer: &mut AssertionWriter,
    simulator: &mut Simulator<32, 64>,
    message: &str,
    source: &str,
) {
    check(
        writer,
        simulator.step().is_ok(),
        message,
        source,
        AssertionDetails::default(),
    );
}

fn check(
    writer: &mut AssertionWriter,
    condition: bool,
    message: &str,
    source: &str,
    details: AssertionDetails<'_>,
) {
    writer
        .record(message, condition, source, details)
        .unwrap_or_else(|error| fail_fast("write assertion event", error));
    assert!(condition, "local assertion failed: {message}");
}

fn argument_value(name: &str) -> Option<String> {
    let mut arguments = env::args().skip(1);
    while let Some(argument) = arguments.next() {
        if argument == name {
            return arguments.next();
        }
    }
    None
}

fn fail_fast<T, E: std::fmt::Debug>(message: &str, error: E) -> T {
    eprintln!("{message}: {error:?}");
    std::process::exit(1);
}

fn fail_missing<T>(message: &str) -> T {
    eprintln!("{message}");
    std::process::exit(1);
}
