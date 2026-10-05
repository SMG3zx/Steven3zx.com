//! User-facing Janus operator CLI.

use std::env;
use std::fmt::Write as FmtWrite;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::PathBuf;

use serde_json::{json, Value};

const DEFAULT_URL: &str = "http://127.0.0.1:8080";
const MAX_RESPONSE_BYTES: usize = 16 * 1024 * 1024;
const MAX_TOKEN_BYTES: usize = 4096;
const TOKEN_READ_ITERATIONS: usize = MAX_TOKEN_BYTES / 256 + 1;
const RESPONSE_READ_ITERATIONS: usize = MAX_RESPONSE_BYTES / 8192 + 1;

fn main() {
    if let Err(error) = run(env::args().skip(1).collect()) {
        eprintln!("janus: {error}");
        std::process::exit(1);
    }
}

fn run(args: Vec<String>) -> Result<(), String> {
    let output_json = args.iter().any(|argument| argument == "--json");
    let filtered: Vec<String> = args
        .into_iter()
        .filter(|argument| argument != "--json")
        .collect();
    if filtered.is_empty() || filtered[0] == "help" || filtered[0] == "--help" {
        print_help();
        return Ok(());
    }
    let base_url = env::var("JANUS_URL") // tigerstyle: allow-direct-env — CLI configuration boundary
        .unwrap_or_else(|_| DEFAULT_URL.to_owned());
    let mut client = Client::new(&base_url, load_token())?;
    client.idempotency_key = option_value(&filtered, "--idempotency-key")?;
    let command = filtered[0].as_str();
    let response = match command {
        "login" | "signin" => login(&client, &filtered[1..], output_json)?,
        "logout" | "signout" => logout(&client)?,
        "projects" => project_command(&client, &filtered[1..], output_json)?,
        "builds" => build_command(&client, &filtered[1..], output_json)?,
        "deployments" => deployment_command(&client, &filtered[1..], output_json)?,
        "runners" => runner_command(&client, &filtered[1..], output_json)?,
        "metrics" => collection(&client, "/api/v1/telemetry", output_json)?,
        "admin-spec" => client.request("GET", "/api/v1/admin/spec", None)?,
        "operations" => operation_command(&client, &filtered[1..], output_json)?,
        "health" => client.request("GET", "/healthz", None)?,
        "readiness" => client.request("GET", "/readyz", None)?,
        _ => return Err(format!("unknown command `{command}`; run `janus help`")),
    };
    if !response.success {
        return Err(format!("HTTP {}: {}", response.status, response.body));
    }
    if output_json || command == "admin-spec" {
        println!("{}", response.body);
    } else {
        println!("{}", pretty_json(&response.body));
    }
    Ok(())
}

fn logout(client: &Client) -> Result<Response, String> {
    let response = client.request("POST", "/api/v1/auth/signout", None)?;
    if response.success {
        if let Err(error) = std::fs::remove_file(token_path()) {
            if error.kind() != std::io::ErrorKind::NotFound {
                return Err(format!("could not remove operator token: {error}"));
            }
        }
    }
    Ok(response)
}

fn login(client: &Client, args: &[String], output_json: bool) -> Result<Response, String> {
    let email =
        option_value(args, "--email")?.ok_or_else(|| "login requires --email".to_owned())?;
    let password =
        option_value(args, "--password")?.ok_or_else(|| "login requires --password".to_owned())?;
    let body = serde_json::to_string(&json!({"email": email, "password": password}))
        .map_err(|error| error.to_string())?;
    let response = client.request("POST", "/api/v1/auth/signin", Some(&body))?;
    if response.success {
        if let Ok(value) = serde_json::from_str::<Value>(&response.body) {
            if let Some(token) = value.get("token").and_then(Value::as_str) {
                save_token(token)?;
            }
        }
        let message = if output_json {
            response.body.clone()
        } else {
            format!(
                "{}\nSet JANUS_TOKEN to the token from this response for subsequent commands.",
                response.body
            )
        };
        return Ok(Response {
            body: message,
            ..response
        });
    }
    Ok(response)
}

fn project_command(
    client: &Client,
    args: &[String],
    output_json: bool,
) -> Result<Response, String> {
    if args.first().is_some_and(|argument| argument == "create") {
        let body = json!({
            "name": required_option(args, "--name")?,
            "slug": required_option(args, "--slug")?,
            "description": option_value(args, "--description")?.unwrap_or_default(),
            "repoProvider": option_value(args, "--repo-provider")?.unwrap_or_else(|| "github".to_owned()),
            "repoUrl": required_option(args, "--repo-url")?,
            "repoBranch": option_value(args, "--repo-branch")?.unwrap_or_else(|| "main".to_owned()),
        });
        return post_json(client, "/api/v1/projects", &body, output_json);
    }
    if args.first().is_some_and(|argument| argument == "update") {
        let project_id = parse_id(args, "--project-id")?;
        let body = json!({
            "name": option_value(args, "--name")?.unwrap_or_default(),
            "slug": option_value(args, "--slug")?.unwrap_or_default(),
            "description": option_value(args, "--description")?.unwrap_or_default(),
            "repoProvider": option_value(args, "--repo-provider")?.unwrap_or_else(|| "github".to_owned()),
            "repoUrl": required_option(args, "--repo-url")?,
            "repoBranch": option_value(args, "--repo-branch")?.unwrap_or_else(|| "main".to_owned()),
        });
        return mutate_json(
            client,
            "PATCH",
            &format!("/api/v1/projects/{project_id}"),
            &body,
            output_json,
        );
    }
    if args.first().is_some_and(|argument| argument == "delete") {
        let project_id = parse_id(args, "--project-id")?;
        return empty_mutation(
            client,
            "DELETE",
            &format!("/api/v1/projects/{project_id}"),
            output_json,
        );
    }
    collection(client, "/api/v1/projects", output_json)
}

fn build_command(client: &Client, args: &[String], output_json: bool) -> Result<Response, String> {
    if args.first().is_some_and(|argument| argument == "submit") {
        let body = json!({
            "projectId": parse_id(args, "--project-id")?,
            "provider": option_value(args, "--provider")?.unwrap_or_else(|| "github".to_owned()),
            "repoUrl": required_option(args, "--repo-url")?,
            "branch": option_value(args, "--branch")?.unwrap_or_else(|| "main".to_owned()),
        });
        return post_json(client, "/api/v1/repos/import", &body, output_json);
    }
    if args.first().is_some_and(|argument| argument == "get") {
        let build_id = parse_id(args, "--build-id")?;
        return collection(client, &format!("/api/v1/builds/{build_id}"), output_json);
    }
    if args.first().is_some_and(|argument| argument == "logs") {
        let build_id = parse_id(args, "--build-id")?;
        return collection(
            client,
            &format!("/api/v1/builds/{build_id}/logs"),
            output_json,
        );
    }
    collection(client, "/api/v1/builds", output_json)
}

fn deployment_command(
    client: &Client,
    args: &[String],
    output_json: bool,
) -> Result<Response, String> {
    if args.first().is_some_and(|argument| argument == "create") {
        let body = json!({
            "projectId": parse_id(args, "--project-id")?,
            "buildId": parse_id(args, "--build-id")?,
            "targetType": option_value(args, "--target-type")?.unwrap_or_else(|| "preview".to_owned()),
            "targetRef": option_value(args, "--target-ref")?.unwrap_or_default(),
            "runnerId": option_value(args, "--runner-id")?.unwrap_or_default(),
        });
        return post_json(client, "/api/v1/deployments", &body, output_json);
    }
    if args.first().is_some_and(|argument| argument == "get") {
        let deployment_id = parse_id(args, "--deployment-id")?;
        return collection(
            client,
            &format!("/api/v1/deployments/{deployment_id}"),
            output_json,
        );
    }
    if args.first().is_some_and(|argument| argument == "delete") {
        let deployment_id = parse_id(args, "--deployment-id")?;
        return empty_mutation(
            client,
            "DELETE",
            &format!("/api/v1/deployments/{deployment_id}"),
            output_json,
        );
    }
    collection(client, "/api/v1/deployments", output_json)
}

fn runner_command(client: &Client, args: &[String], output_json: bool) -> Result<Response, String> {
    match args.first().map(String::as_str) {
        Some("register") => {
            let body = json!({
                "id": parse_id(args, "--runner-id")?,
                "capabilities": option_value(args, "--capabilities")?
                    .unwrap_or_default()
                    .split(',')
                    .filter(|value| !value.trim().is_empty())
                    .map(|value| value.trim().to_owned())
                    .collect::<Vec<_>>(),
                "lease_seconds": parse_optional_id(args, "--lease-seconds")?.unwrap_or(60),
            });
            post_json(client, "/api/v1/runners/register", &body, output_json)
        }
        Some("heartbeat") => {
            let body = json!({
                "id": parse_id(args, "--runner-id")?,
                "lease_seconds": parse_optional_id(args, "--lease-seconds")?.unwrap_or(60),
            });
            post_json(client, "/api/v1/runners/heartbeat", &body, output_json)
        }
        _ => collection(client, "/api/v1/runners", output_json),
    }
}

fn operation_command(
    client: &Client,
    args: &[String],
    output_json: bool,
) -> Result<Response, String> {
    if args.first().is_some_and(|argument| argument == "get") {
        let operation_id = required_option(args, "--operation-id")?;
        return collection(
            client,
            &format!("/api/v1/operations/{operation_id}"),
            output_json,
        );
    }
    Err("operations requires `get --operation-id <id>`".to_owned())
}

fn post_json(
    client: &Client,
    path: &str,
    value: &Value,
    output_json: bool,
) -> Result<Response, String> {
    let body = serde_json::to_string(&value).map_err(|error| error.to_string())?;
    let response = client.request("POST", path, Some(&body))?;
    if response.success && !output_json {
        return Ok(Response {
            body: pretty_json(&response.body),
            ..response
        });
    }
    Ok(response)
}

fn mutate_json(
    client: &Client,
    method: &str,
    path: &str,
    value: &Value,
    output_json: bool,
) -> Result<Response, String> {
    let body = serde_json::to_string(value).map_err(|error| error.to_string())?;
    let response = client.request(method, path, Some(&body))?;
    if response.success && !output_json {
        return Ok(Response {
            body: pretty_json(&response.body),
            ..response
        });
    }
    Ok(response)
}

fn empty_mutation(
    client: &Client,
    method: &str,
    path: &str,
    output_json: bool,
) -> Result<Response, String> {
    let response = client.request(method, path, None)?;
    if output_json || !response.success || !response.body.is_empty() {
        return Ok(response);
    }
    Ok(Response {
        body: "{\"status\":\"ok\"}".to_owned(),
        ..response
    })
}

fn collection(client: &Client, path: &str, output_json: bool) -> Result<Response, String> {
    let response = client.request("GET", path, None)?;
    if output_json || !response.success {
        return Ok(response);
    }
    Ok(Response {
        body: pretty_json(&response.body),
        ..response
    })
}

fn option_value(args: &[String], name: &str) -> Result<Option<String>, String> {
    let Some(index) = args.iter().position(|argument| argument == name) else {
        return Ok(None);
    };
    args.get(index + 1)
        .cloned()
        .map(Some)
        .ok_or_else(|| format!("{name} requires a value"))
}

fn required_option(args: &[String], name: &str) -> Result<String, String> {
    option_value(args, name)?.ok_or_else(|| format!("command requires {name}"))
}

fn parse_id(args: &[String], name: &str) -> Result<u64, String> {
    required_option(args, name)?
        .parse::<u64>()
        .map_err(|_| format!("{name} must be an integer"))
}

fn parse_optional_id(args: &[String], name: &str) -> Result<Option<u64>, String> {
    option_value(args, name)?
        .map(|value| {
            value
                .parse::<u64>()
                .map_err(|_| format!("{name} must be an integer"))
        })
        .transpose()
}

fn token_path() -> PathBuf {
    env::var_os("JANUS_TOKEN_FILE").map_or_else(
        || PathBuf::from("janus-rust/artifacts/operator-token"),
        PathBuf::from,
    )
}

fn load_token() -> Option<String> {
    env::var("JANUS_TOKEN") // tigerstyle: allow-direct-env — CLI credential boundary
        .ok()
        .or_else(|| read_token_file().ok())
}

fn read_token_file() -> Result<String, String> {
    let mut file = std::fs::File::open(token_path()).map_err(|error| error.to_string())?;
    let length = file.metadata().map_err(|error| error.to_string())?.len();
    let maximum = u64::try_from(MAX_TOKEN_BYTES).map_err(|_| "token bound overflow".to_owned())?;
    if length > maximum {
        return Err("operator token file exceeds the safety bound".to_owned());
    }
    let mut bytes = Vec::with_capacity(MAX_TOKEN_BYTES);
    let mut chunk = [0_u8; 256];
    for _ in 0..TOKEN_READ_ITERATIONS {
        let count = file.read(&mut chunk).map_err(|error| error.to_string())?;
        if count == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..count]);
    }
    String::from_utf8(bytes)
        .map(|token| token.trim().to_owned())
        .map_err(|error| error.to_string())
}

fn save_token(token: &str) -> Result<(), String> {
    let path = token_path();
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)
            .map_err(|error| format!("could not create token directory: {error}"))?;
    }
    std::fs::write(path, token).map_err(|error| format!("could not save operator token: {error}"))
}

fn pretty_json(body: &str) -> String {
    serde_json::from_str::<Value>(body)
        .ok()
        .and_then(|value| serde_json::to_string_pretty(&value).ok())
        .unwrap_or_else(|| body.to_owned())
}

fn print_help() {
    print!(
        r"Janus operator CLI

Usage:
  janus <command> [options]

Commands:
  login, signin       Authenticate and save a local session
  logout, signout     Revoke the session and remove the local token
  projects            List projects; use `projects create` to create one
  builds              List builds; use `builds submit|get|logs` for details
  deployments         List deployments; use `deployments create|get|delete`
  runners             List, register, or heartbeat runners
  metrics             Show telemetry and metrics
  admin-spec          Fetch the administrator system specification
  operations          Read a durable operation by id
  health              Check liveness
  readiness           Check readiness

Options:
  --json              Emit machine-readable JSON
  --email <email>     Login email
  --password <pass>   Login password
  --name <name>       Project name
  --slug <slug>       Project slug
  --repo-url <url>    Repository URL
  --project-id <id>   Project identity
  --build-id <id>     Build identity
  --deployment-id <id>
                       Deployment identity
  --runner-id <id>    Runner identity
  --operation-id <id> Durable operation identity
  --lease-seconds <n> Runner lease duration
  --capabilities <a,b> Runner capability list
  --idempotency-key <key>
                       Stable retry identity for mutation commands

Environment:
  JANUS_URL           API base URL (default: http://127.0.0.1:8080)
  JANUS_TOKEN         Bearer token for authenticated commands
  JANUS_TOKEN_FILE    Local token path (default: janus-rust/artifacts/operator-token)"
    );
}

struct Client {
    host: String,
    port: u16,
    base_path: String,
    token: Option<String>,
    idempotency_key: Option<String>,
}

impl Client {
    fn new(base_url: &str, token: Option<String>) -> Result<Self, String> {
        let url = base_url
            .strip_prefix("http://")
            .ok_or_else(|| "JANUS_URL must use http:// for the local CLI".to_owned())?;
        let (authority, base_path) = url.split_once('/').unwrap_or((url, ""));
        let (host, port) = authority
            .split_once(':')
            .map_or((authority, 80), |(host, port)| {
                (host, port.parse::<u16>().unwrap_or(80))
            });
        if host.is_empty() {
            return Err("JANUS_URL has no host".to_owned());
        }
        Ok(Self {
            host: host.to_owned(),
            port,
            base_path: format!("/{base_path}").trim_end_matches('/').to_owned(),
            token,
            idempotency_key: None,
        })
    }

    fn request(&self, method: &str, path: &str, body: Option<&str>) -> Result<Response, String> {
        let mut stream = TcpStream::connect((&*self.host, self.port))
            .map_err(|error| format!("connect failed: {error}"))?;
        let request_path = format!("{}{}", self.base_path, path);
        let payload = body.unwrap_or("");
        let mut request = format!(
            "{method} {request_path} HTTP/1.1\r\nHost: {}\r\nConnection: close\r\nAccept: application/json\r\n",
            self.host
        );
        if let Some(token) = &self.token {
            write!(request, "Authorization: Bearer {token}\r\n")
                .map_err(|_| "request formatting failed".to_owned())?;
        }
        if let Some(key) = &self.idempotency_key {
            write!(request, "Idempotency-Key: {key}\r\n")
                .map_err(|_| "request formatting failed".to_owned())?;
        }
        if !payload.is_empty() {
            write!(
                request,
                "Content-Type: application/json\r\nContent-Length: {}\r\n",
                payload.len()
            )
            .map_err(|_| "request formatting failed".to_owned())?;
        }
        request.push_str("\r\n");
        request.push_str(payload);
        stream
            .write_all(request.as_bytes())
            .map_err(|error| format!("request failed: {error}"))?;
        let mut bytes = Vec::with_capacity(MAX_RESPONSE_BYTES);
        let mut chunk = [0_u8; 8192];
        for _ in 0..RESPONSE_READ_ITERATIONS {
            let count = stream
                .read(&mut chunk)
                .map_err(|error| format!("response failed: {error}"))?;
            if count == 0 {
                break;
            }
            if bytes.len().saturating_add(count) > MAX_RESPONSE_BYTES {
                return Err("response exceeded the CLI safety bound".to_owned());
            }
            bytes.extend_from_slice(&chunk[..count]);
        }
        let raw =
            String::from_utf8(bytes).map_err(|error| format!("response was not UTF-8: {error}"))?;
        let (headers, body) = raw
            .split_once("\r\n\r\n")
            .ok_or_else(|| "invalid HTTP response".to_owned())?;
        let status = headers
            .lines()
            .next()
            .and_then(|line| line.split_whitespace().nth(1))
            .and_then(|value| value.parse().ok())
            .ok_or_else(|| "invalid HTTP status".to_owned())?;
        Ok(Response {
            status,
            success: (200..300).contains(&status),
            body: body.to_owned(),
        })
    }
}

struct Response {
    status: u16,
    success: bool,
    body: String,
}

#[cfg(test)]
mod tests {
    use super::{option_value, pretty_json, Client, DEFAULT_URL};

    #[test]
    fn default_client_uses_local_control_plane() {
        let client = Client::new(DEFAULT_URL, None).expect("default URL parses");
        assert_eq!(client.host, "127.0.0.1");
        assert_eq!(client.port, 8080);
        assert_eq!(client.base_path, "");
    }

    #[test]
    fn options_require_a_following_value() {
        let args = vec!["--email".to_owned()];
        assert!(option_value(&args, "--email").is_err());
    }

    #[test]
    fn json_responses_are_pretty_printed_without_corrupting_plain_text() {
        assert_eq!(pretty_json(r#"{"ok":true}"#), "{\n  \"ok\": true\n}");
        assert_eq!(pretty_json("plain text"), "plain text");
    }
}
