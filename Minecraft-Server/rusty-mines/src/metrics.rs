use std::collections::VecDeque;
use std::io;
use std::sync::{mpsc, Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};
use sysinfo::{ProcessRefreshKind, ProcessesToUpdate, System};

const WINDOW: Duration = Duration::from_secs(60);
const INTERVAL: Duration = Duration::from_secs(1);

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct ProcessStats {
    pub cpu_percent: Option<f64>,
    pub memory_mib: Option<f64>,
    pub samples: usize,
}

#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct TrafficStats {
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub rx_frames: u64,
    pub tx_frames: u64,
}

#[derive(Default)]
pub(crate) struct Traffic {
    received: std::sync::atomic::AtomicU64,
    transmitted: std::sync::atomic::AtomicU64,
    inbound: std::sync::atomic::AtomicU64,
    outbound: std::sync::atomic::AtomicU64,
}
impl Traffic {
    pub(crate) fn inbound_packet(&self) {
        self.inbound
            .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }
    pub(crate) fn snapshot(&self) -> [u64; 4] {
        use std::sync::atomic::Ordering::Relaxed;
        [
            self.received.load(Relaxed),
            self.transmitted.load(Relaxed),
            self.inbound.load(Relaxed),
            self.outbound.load(Relaxed),
        ]
    }
    pub(crate) fn stats(&self) -> TrafficStats {
        let [rx_bytes, tx_bytes, rx_frames, tx_frames] = self.snapshot();
        TrafficStats {
            rx_bytes,
            tx_bytes,
            rx_frames,
            tx_frames,
        }
    }
}

// Count successful socket I/O, including partial writes. The outbound framing
// cursor handles codecs that split a frame across multiple write_all calls.
pub(crate) struct CountingIo<T> {
    pub(crate) inner: T,
    pub(crate) traffic: Arc<Traffic>,
    length: u32,
    shift: u32,
    remaining: usize,
}
impl<T> CountingIo<T> {
    pub(crate) fn new(inner: T, traffic: Arc<Traffic>) -> Self {
        Self {
            inner,
            traffic,
            length: 0,
            shift: 0,
            remaining: 0,
        }
    }
}
impl<T: io::Read> io::Read for CountingIo<T> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let n = self.inner.read(buf)?;
        self.traffic
            .received
            .fetch_add(n as u64, std::sync::atomic::Ordering::Relaxed);
        Ok(n)
    }
}
impl<T: io::Write> io::Write for CountingIo<T> {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let n = self.inner.write(buf)?;
        self.traffic
            .transmitted
            .fetch_add(n as u64, std::sync::atomic::Ordering::Relaxed);
        let mut bytes = &buf[..n];
        while !bytes.is_empty() {
            if self.remaining > 0 {
                let count = self.remaining.min(bytes.len());
                self.remaining -= count;
                bytes = &bytes[count..];
                if self.remaining == 0 {
                    self.traffic
                        .outbound
                        .fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                }
            } else {
                let byte = bytes[0];
                bytes = &bytes[1..];
                self.length |= u32::from(byte & 127) << self.shift;
                if byte & 128 == 0 {
                    self.remaining = self.length as usize;
                    self.length = 0;
                    self.shift = 0;
                } else {
                    self.shift += 7;
                }
            }
        }
        Ok(n)
    }
    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

fn normalized_cpu(cpu: f32, logical_cpus: usize) -> f64 {
    (f64::from(cpu) / logical_cpus.max(1) as f64).clamp(0.0, 100.0)
}

fn mib(bytes: u64) -> f64 {
    bytes as f64 / 1_048_576.0
}

#[derive(Default)]
struct RollingSamples(VecDeque<(Instant, f64, u64)>);

impl RollingSamples {
    fn prune(&mut self, now: Instant) {
        while self
            .0
            .front()
            .is_some_and(|sample| now.duration_since(sample.0) >= WINDOW)
        {
            self.0.pop_front();
        }
    }

    fn push(&mut self, now: Instant, cpu: f64, memory: u64) {
        self.prune(now);
        if self.0.len() == 60 {
            self.0.pop_front();
        }
        self.0.push_back((now, cpu, memory));
    }

    fn average(&mut self, now: Instant) -> Option<(f64, f64, usize)> {
        self.prune(now);
        let count = self.0.len();
        if count == 0 {
            return None;
        }
        Some((
            self.0.iter().map(|s| s.1).sum::<f64>() / count as f64,
            self.0.iter().map(|s| mib(s.2)).sum::<f64>() / count as f64,
            count,
        ))
    }
}

#[derive(Default)]
struct State {
    samples: RollingSamples,
}

pub(crate) struct Metrics {
    state: Arc<Mutex<State>>,
    stop: mpsc::Sender<()>,
    worker: Option<JoinHandle<()>>,
}

impl Metrics {
    pub(crate) fn start() -> io::Result<Self> {
        let pid = sysinfo::get_current_pid().map_err(io::Error::other)?;

        let state = Arc::new(Mutex::new(State::default()));
        let shared = state.clone();
        let (stop, wait) = mpsc::channel();
        let worker = thread::Builder::new()
            .name("local-metrics".into())
            .spawn(move || {
                let mut system = System::new();
                let refresh = ProcessRefreshKind::nothing().with_cpu().with_memory();
                // Logical processors, not physical cores, define whole-machine capacity.
                system.refresh_cpu_list(sysinfo::CpuRefreshKind::nothing());
                let logical_cpus = system.cpus().len().max(1);
                system.refresh_processes_specifics(ProcessesToUpdate::Some(&[pid]), true, refresh);
                // The first process refresh establishes CPU baseline; it is not a sample.
                while matches!(
                    wait.recv_timeout(INTERVAL),
                    Err(mpsc::RecvTimeoutError::Timeout)
                ) {
                    system.refresh_processes_specifics(
                        ProcessesToUpdate::Some(&[pid]),
                        true,
                        refresh,
                    );
                    let now = Instant::now();
                    let mut state = shared.lock().unwrap_or_else(|e| e.into_inner());
                    if let Some(process) = system.process(pid) {
                        state.samples.push(
                            now,
                            normalized_cpu(process.cpu_usage(), logical_cpus),
                            process.memory(),
                        );
                    }
                }
            })?;
        Ok(Self {
            state,
            stop,
            worker: Some(worker),
        })
    }

    pub(crate) fn snapshot(&self) -> ProcessStats {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        match state.samples.average(Instant::now()) {
            Some((cpu, memory, samples)) => ProcessStats {
                cpu_percent: Some(cpu),
                memory_mib: Some(memory),
                samples,
            },
            None => ProcessStats::default(),
        }
    }
}

impl Drop for Metrics {
    fn drop(&mut self) {
        let _ = self.stop.send(());
        if let Some(worker) = self.worker.take() {
            if worker.join().is_err() {
                crate::console_log("Local metrics sampler panicked; stopped and joined".into());
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn counts_actual_partial_io_and_complete_outbound_frames() {
        use std::io::{Read, Write};
        use std::sync::atomic::Ordering::Relaxed;
        struct LimitedWriter {
            remaining: usize,
        }
        impl Write for LimitedWriter {
            fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
                if self.remaining == 0 {
                    return Err(io::Error::new(io::ErrorKind::BrokenPipe, "closed"));
                }
                let count = bytes.len().min(self.remaining).min(2);
                self.remaining -= count;
                Ok(count)
            }
            fn flush(&mut self) -> io::Result<()> {
                Ok(())
            }
        }
        let traffic = Arc::new(Traffic::default());
        let mut writer = CountingIo::new(LimitedWriter { remaining: 5 }, traffic.clone());
        // First frame completes, second frame fails after its length and ID.
        assert!(writer.write_all(&[2, 0, 7, 2, 1, 8]).is_err());
        assert_eq!(traffic.transmitted.load(Relaxed), 5);
        assert_eq!(traffic.outbound.load(Relaxed), 1);
        let mut reader = CountingIo::new(io::Cursor::new(vec![1, 2, 3]), traffic.clone());
        let mut bytes = Vec::new();
        reader.read_to_end(&mut bytes).unwrap();
        assert_eq!(traffic.received.load(Relaxed), 3);
        assert_eq!(traffic.inbound.load(Relaxed), 0);
        assert_eq!(Traffic::default().snapshot(), [0; 4]);
    }

    #[test]
    fn fragmented_outbound_headers_and_multiple_clients_are_counted_once() {
        use std::io::Write;
        let traffic = Arc::new(Traffic::default());
        let mut writer = CountingIo::new(Vec::new(), traffic.clone());
        writer.write_all(&[0x80]).unwrap();
        assert_eq!(traffic.snapshot(), [0, 1, 0, 0]);
        writer.write_all(&[1]).unwrap();
        writer.write_all(&[0; 127]).unwrap();
        assert_eq!(traffic.snapshot(), [0, 129, 0, 0]);
        writer.write_all(&[0]).unwrap();
        assert_eq!(traffic.snapshot(), [0, 130, 0, 1]);
        let mut other = CountingIo::new(Vec::new(), traffic.clone());
        other.write_all(&[1, 0, 1, 0]).unwrap();
        assert_eq!(traffic.snapshot(), [0, 134, 0, 3]);
    }

    #[test]
    fn rolling_average_prunes_at_boundary_and_is_bounded() {
        let start = Instant::now();
        let mut samples = RollingSamples::default();
        assert!(samples.average(start).is_none());
        samples.push(start, 10.0, 1_048_576);
        samples.push(start + INTERVAL, 30.0, 3 * 1_048_576);
        assert_eq!(samples.average(start + INTERVAL), Some((20.0, 2.0, 2)));
        assert_eq!(samples.average(start + WINDOW), Some((30.0, 3.0, 1)));
        assert!(samples.average(start + WINDOW + INTERVAL).is_none());
        for _ in 0..100 {
            samples.push(start + WINDOW + INTERVAL, 1.0, 0);
        }
        assert_eq!(samples.0.len(), 60);
    }

    #[test]
    fn guard_wakes_and_joins_on_error_and_unwind() {
        use std::sync::atomic::{AtomicBool, Ordering};
        fn guard(done: Arc<AtomicBool>) -> Metrics {
            let (stop, wait) = mpsc::channel();
            let worker = thread::spawn(move || {
                let _ = wait.recv();
                done.store(true, Ordering::SeqCst);
            });
            Metrics {
                state: Arc::new(Mutex::new(State::default())),
                stop,
                worker: Some(worker),
            }
        }
        let done = Arc::new(AtomicBool::new(false));
        let result: io::Result<()> = {
            let metrics = guard(done.clone());
            assert_eq!(metrics.snapshot(), ProcessStats::default());
            Err(io::Error::other("startup/UI error"))
        };
        assert!(result.is_err());
        assert!(done.load(Ordering::SeqCst));
        done.store(false, Ordering::SeqCst);
        let result = std::panic::catch_unwind(|| {
            let _metrics = guard(done.clone());
            panic!("UI panic");
        });
        assert!(result.is_err());
        assert!(done.load(Ordering::SeqCst));
    }

    #[test]
    fn cpu_and_memory_units() {
        assert_eq!(normalized_cpu(200.0, 8), 25.0);
        assert_eq!(normalized_cpu(900.0, 8), 100.0);
        assert_eq!(normalized_cpu(-1.0, 0), 0.0);
        assert_eq!(mib(1_572_864), 1.5);
    }
}
