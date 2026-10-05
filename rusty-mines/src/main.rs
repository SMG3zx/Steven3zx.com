mod config;
mod connection;
mod console;
mod dashboard;
mod data_type;
#[cfg(test)]
mod foundation_live_tests;
mod gateway;
mod gateway_credentials;
mod gateway_dashboard;
mod metrics;
mod module_bindings;
mod packets;
mod players;
mod world_dashboard;

use crate::connection::handle_controlled_client;

use reedline::{DefaultCompleter, Reedline};

use std::io;
use std::net::{Shutdown, TcpListener, TcpStream};
use std::sync::{
    atomic::{AtomicBool, AtomicUsize, Ordering},
    mpsc::{self, SyncSender},
    Arc, Mutex, OnceLock,
};
use std::thread;
use std::time::{Duration, Instant};

static OUTPUT: OnceLock<SyncSender<console::LogRecord>> = OnceLock::new();
static DROPPED_LOGS: AtomicUsize = AtomicUsize::new(0);

pub(crate) fn take_dropped_logs() -> usize {
    DROPPED_LOGS.swap(0, Ordering::Relaxed)
}

pub(crate) fn console_log(message: String) {
    console_record(console::LogRecord::plain(message));
}

pub(crate) fn console_record(mut message: console::LogRecord) {
    if let Some(printer) = OUTPUT.get() {
        // Never block a network worker when the console is busy or exiting.
        // Bound queued message size, not just the number of queued messages.
        message.text = message.text.chars().take(32768).collect();
        if let Err(mpsc::TrySendError::Full(_)) = printer.try_send(message) {
            DROPPED_LOGS.fetch_add(1, Ordering::Relaxed);
        }
    } else {
        println!("{}", message.text);
    }
}

struct ShutdownGuard<F: FnOnce()>(Option<F>);
impl<F: FnOnce()> Drop for ShutdownGuard<F> {
    fn drop(&mut self) {
        if let Some(shutdown) = self.0.take() {
            shutdown();
        }
    }
}

fn main() -> io::Result<()> {
    dotenvy::dotenv().ok();
    let mut editor =
        Reedline::create().with_completer(Box::new(DefaultCompleter::new_with_wordlen(
            console::COMMANDS
                .iter()
                .map(|command| (*command).to_owned())
                .collect(),
            1,
        )));
    let (config, config_path) = match config::load_or_setup(&mut editor) {
        Err(error) if error.kind() == io::ErrorKind::Interrupted => return Ok(()),
        result => result?,
    };
    let traffic = Arc::new(metrics::Traffic::default());
    let metrics = metrics::Metrics::start()?;
    let listener = TcpListener::bind(config.listen)?;
    listener.set_nonblocking(true)?;
    let address = listener.local_addr()?;
    let (sender, receiver) = mpsc::sync_channel(1024);
    let _ = OUTPUT.set(sender);

    let controls = Arc::new(players::Registry::default());
    let gateway = gateway::Gateway::connect(&config.host, &config.database, controls.clone());

    let stopping = Arc::new(AtomicBool::new(false));
    let sockets = Arc::new(Mutex::new(Vec::<(u64, TcpStream)>::new()));
    let worker_stop = stopping.clone();
    let worker_sockets = sockets.clone();
    let listener_traffic = traffic.clone();
    let listener_controls = controls.clone();
    let listener_gateway = gateway.clone();
    let listener_thread = thread::spawn(move || {
        let mut next_id = 0u64;
        while !worker_stop.load(Ordering::Relaxed) {
            match listener.accept() {
                Ok((stream, _)) => {
                    let socket = match stream.try_clone() {
                        Ok(socket) => socket,
                        Err(error) => {
                            console_log(format!("Cannot track client: {error}"));
                            continue;
                        }
                    };
                    let id = next_id;
                    next_id = next_id.wrapping_add(1);
                    worker_sockets.lock().unwrap().push((id, socket));
                    let clients = worker_sockets.clone();
                    let traffic = listener_traffic.clone();
                    let gateway = listener_gateway.clone();
                    let controls = listener_controls.clone();
                    thread::spawn(move || {
                        if let Err(error) =
                            handle_controlled_client(stream, traffic, gateway, controls)
                        {
                            console_log(format!("Error handling client: {error}"));
                        }
                        clients
                            .lock()
                            .unwrap()
                            .retain(|(client_id, _)| *client_id != id);
                    });
                }
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(50))
                }
                Err(error) if error.kind() == io::ErrorKind::Interrupted => continue,
                Err(error) => {
                    console_log(format!("Listener error: {error}"));
                    break;
                }
            }
        }
    });
    let shutdown = ShutdownGuard(Some(|| {
        stopping.store(true, Ordering::Relaxed);
        let _ = listener_thread.join();
        for (_, stream) in sockets
            .lock()
            .unwrap_or_else(|error| error.into_inner())
            .iter()
        {
            let _ = stream.shutdown(Shutdown::Both);
        }
        gateway.disconnect();
    }));
    let console_result = (|| -> io::Result<()> {
        let mut console = console::Console::new(receiver)?;
        console.log(&format!("Configuration: {}", config_path.display()));
        console.log(&format!(
            "Server listening on {address}. Type help for commands."
        ));
        let started = Instant::now();
        let snapshot = || dashboard::StatusSnapshot {
            listen: address.to_string(),
            database: config.database.clone(),
            host: config.host.clone(),
            gateway: gateway.status(),
            sockets: sockets.lock().unwrap_or_else(|e| e.into_inner()).len(),
            uptime_secs: started.elapsed().as_secs(),
            players: gateway.play_count(&controls),
            process: metrics.snapshot(),
            traffic: traffic.stats(),
        };
        let player_snapshot = || gateway.players(&controls);
        let gateway_snapshot =
            |players| gateway.diagnostics(&config.host, &config.database, players);
        while let Some(line) = console.read_command(snapshot, player_snapshot, gateway_snapshot)? {
            if line.trim().is_empty() {
                continue;
            }
            console.clear();
            match line.trim() {
                "help" => console.log(
                    "help    Show commands
status  Live server dashboard
players Live local player sessions and measured RTT
world   Static world metadata and local Play count
gateway Live SDK health, registration and local session counts
kick    Select a local player and disconnect with a reason
broadcast    Prompt for a system message to local confirmed Play players
logs    Retained history, following new messages
config  Edit saved settings (restart to apply)
clear   Clear displayed view (retain logs)
stop or exit    Shut down the server
Up/Down: history; Tab: complete; PgUp/PgDn: scroll; Ctrl-End: latest",
                ),
                "config" => {
                    match console.suspend(|| config::edit_settings(&mut editor, &config_path))? {
                        Ok(()) => {
                            console.log("Configuration saved; restart to apply (no hot reload).")
                        }
                        Err(error) if error.kind() == io::ErrorKind::Interrupted => {
                            console.log("Configuration editing cancelled.")
                        }
                        Err(error) => {
                            console.log(&format!("Could not edit configuration: {error}"))
                        }
                    }
                }
                "broadcast" => match console.suspend(|| players::prompt_broadcast(&mut editor))? {
                    Ok(text) => match controls.broadcast(&player_snapshot(), &text) {
                        Ok(deliveries) => {
                            console.log(&format!("Broadcast queued for {} local Play players; awaiting socket writes.", deliveries.len()));
                            thread::spawn(move || {
                                let mut sent = 0;
                                let mut failed = 0;
                                let mut pending = 0;
                                let deadline = Instant::now() + Duration::from_secs(6);
                                for (_, ack) in deliveries {
                                    match ack.recv_timeout(
                                        deadline.saturating_duration_since(Instant::now()),
                                    ) {
                                        Ok(Ok(())) => sent += 1,
                                        Ok(Err(_)) | Err(mpsc::RecvTimeoutError::Disconnected) => {
                                            failed += 1
                                        }
                                        Err(mpsc::RecvTimeoutError::Timeout) => pending += 1,
                                    }
                                }
                                console_log(format!("Broadcast: {sent} socket writes completed, {failed} failed, {pending} unconfirmed (not client-read receipts)."));
                            });
                        }
                        Err(error) => console.log(&format!("Broadcast not issued: {error}")),
                    },
                    Err(error) if error.kind() == io::ErrorKind::Interrupted => {
                        console.log("Broadcast cancelled; server is still running.")
                    }
                    Err(error) => console.log(&format!("Broadcast not issued: {error}")),
                },
                "players" => console.players(player_snapshot()),
                "world" => console.world(world_dashboard::Snapshot {
                    players: player_snapshot(),
                }),
                "gateway" => console.gateway(gateway_snapshot(player_snapshot())),
                "kick" => {
                    let selected = player_snapshot();
                    match console.suspend(|| players::prompt(&mut editor, &selected))? {
                        Ok((id, reason)) => {
                            let current = player_snapshot();
                            if !current.rows.iter().any(|p| p.id == id) {
                                console.log(
                                    "Player left or is no longer locally owned; no kick issued.",
                                );
                                continue;
                            }
                            match controls.issue(id, reason) {
                                Ok(ack) => {
                                    console.log(
                                        "Kick issued; awaiting worker disconnect confirmation.",
                                    );
                                    thread::spawn(move || {
                                        match ack.recv_timeout(Duration::from_secs(5)) {
                                            Ok(Ok(())) => console_log(format!("Session {id}: disconnect completed; backend cleanup requested.")),
                                            Ok(Err(error)) => console_log(format!("Session {id}: kick failed: {error}")),
                                            Err(mpsc::RecvTimeoutError::Timeout) => {
                                                console_log(format!("Session {id}: kick still pending after 5s; not confirmed."));
                                                match ack.recv() {
                                                    Ok(Ok(())) => console_log(format!("Session {id}: deferred disconnect completed; backend cleanup requested.")),
                                                    Ok(Err(error)) => console_log(format!("Session {id}: deferred kick failed: {error}")),
                                                    Err(_) => console_log(format!("Session {id}: worker closed without confirmation.")),
                                                }
                                            }
                                            Err(_) => console_log(format!("Session {id}: worker closed without confirmation.")),
                                        }
                                    });
                                }
                                Err(error) => console.log(&format!("Kick not issued: {error}")),
                            }
                        }
                        Err(error) if error.kind() == io::ErrorKind::Interrupted => {
                            console.log("Kick cancelled; server is still running.")
                        }
                        Err(error) => console.log(&format!("Kick not issued: {error}")),
                    }
                }
                "status" => console.status(snapshot()),
                "logs" => console.logs(),
                "clear" => console.clear(),
                "stop" | "exit" => {
                    console.log("Stopping server...");
                    console.draw()?;
                    break;
                }
                other => console.log(&format!(
                    "Unknown command: {other}. Type help for commands."
                )),
            }
        }
        Ok(())
    })();
    println!("Stopping server...");
    drop(shutdown);

    console_result
}

#[cfg(test)]
mod shutdown_tests {
    use super::ShutdownGuard;
    use std::cell::Cell;

    #[test]
    fn shutdown_runs_on_error_and_unwind() {
        let called = Cell::new(0);
        let result: std::io::Result<()> = {
            let _guard = ShutdownGuard(Some(|| called.set(called.get() + 1)));
            Err(std::io::Error::other("UI failed"))
        };
        assert!(result.is_err());
        assert_eq!(called.get(), 1);
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let _guard = ShutdownGuard(Some(|| called.set(called.get() + 1)));
            panic!("UI panicked");
        }));
        assert!(result.is_err());
        assert_eq!(called.get(), 2);
    }
}
