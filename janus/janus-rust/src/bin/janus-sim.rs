//! Small executable smoke test for the deterministic Janus vertical slice.

use janus_core::{BuildState, Command, CommandId, EntityId, Simulator, TenantId};

fn main() {
    let mut simulator = Simulator::<32, 64>::new();
    if simulator
        .submit(Command::SubmitBuild {
            command_id: CommandId(1),
            build: EntityId(1),
            tenant: TenantId(1),
        })
        .is_err()
    {
        eprintln!("simulation command admission failed");
        return;
    }
    if simulator.step().is_err() || simulator.step().is_err() {
        eprintln!("simulation journal capacity reached");
        return;
    }
    let state = simulator.world.build(EntityId(1)).map(|build| build.state);
    assert_eq!(state, Some(BuildState::Succeeded));
    println!(
        "janus-sim: build-1 reached {:?} in {} ticks",
        state,
        simulator.performance_samples().len()
    );
}
