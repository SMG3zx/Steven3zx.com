use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    collections::BTreeMap,
    error::Error,
    fs::File,
    io::{self, Read},
    time::Duration,
};

const ENDPOINT: &str = "https://api.typesafe.ai/v1/systemone";
const MAX_BYTES: u64 = 1_048_576;

#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    state: Value,
    #[serde(default = "default_model")]
    model: String,
    questions: BTreeMap<String, Question>,
}
fn default_model() -> String {
    "jev-latest".into()
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase", deny_unknown_fields)]
enum Question {
    Noul {
        instructions: Value,
        #[serde(skip_serializing_if = "Option::is_none")]
        criteria: Option<BTreeMap<String, Value>>,
    },
    Choice {
        instructions: Value,
        criteria: BTreeMap<String, Value>,
    },
    Score {
        instructions: Value,
        criteria: Vec<Value>,
    },
}

#[derive(Debug, Serialize, Deserialize)]
struct Response {
    model: String,
    answers: BTreeMap<String, Answer>,
    usage: Value,
}
#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
enum Answer {
    Noul {
        noul: f64,
    },
    Choice {
        choice: String,
        probabilities: BTreeMap<String, f64>,
        confidence: f64,
    },
    Score {
        score: f64,
        legend: BTreeMap<String, Value>,
        probabilities: BTreeMap<String, f64>,
        confidence: f64,
    },
}
fn probability(value: f64) -> bool {
    value.is_finite() && (0.0..=1.0).contains(&value)
}
fn distribution(values: &BTreeMap<String, f64>) -> bool {
    !values.is_empty()
        && values.values().all(|v| probability(*v))
        && (values.values().sum::<f64>() - 1.0).abs() <= 0.01
}
fn description(value: &Value) -> bool {
    match value {
        Value::String(s) => !s.trim().is_empty(),
        Value::Object(v) => !v.is_empty(),
        Value::Array(v) => !v.is_empty(),
        _ => false,
    }
}
impl Request {
    fn validate(&self) -> Result<(), &'static str> {
        if self.model.trim().is_empty() || self.questions.is_empty() || self.questions.len() > 64 {
            return Err("Provide a model and 1–64 questions (harness safety cap)");
        }
        for (id, question) in &self.questions {
            if id.trim().is_empty() {
                return Err("Question IDs cannot be empty");
            }
            let instructions = match question {
                Question::Noul {
                    instructions,
                    criteria,
                } => {
                    if let Some(c) = criteria {
                        if c.len() != 2
                            || !c.contains_key("true")
                            || !c.contains_key("false")
                            || !c.values().all(description)
                        {
                            return Err("Noul criteria must describe true and false");
                        }
                    }
                    instructions
                }
                Question::Choice {
                    instructions,
                    criteria,
                } => {
                    if criteria.len() < 2
                        || criteria.len() > 255
                        || criteria.keys().any(|k| k.is_empty())
                        || !criteria.values().all(|v| v.is_null() || description(v))
                    {
                        return Err(
                            "Choice requires 2–255 named options with descriptions or null",
                        );
                    }
                    instructions
                }
                Question::Score {
                    instructions,
                    criteria,
                } => {
                    if !(2..=10).contains(&criteria.len()) || !criteria.iter().all(description) {
                        return Err("Score requires 2–10 descriptive levels");
                    }
                    instructions
                }
            };
            if !description(instructions) {
                return Err("Instructions must be a nonempty string, object or array");
            }
        }
        Ok(())
    }
    fn validate_response(&self, response: &Response) -> Result<(), &'static str> {
        if self.questions.len() != response.answers.len() {
            return Err("Response question count mismatch");
        }
        for (id, question) in &self.questions {
            let valid = match (question, response.answers.get(id)) {
                (Question::Noul { .. }, Some(Answer::Noul { noul })) => probability(*noul),
                (
                    Question::Choice { criteria, .. },
                    Some(Answer::Choice {
                        choice,
                        probabilities,
                        confidence,
                    }),
                ) => {
                    criteria.contains_key(choice)
                        && criteria.keys().eq(probabilities.keys())
                        && distribution(probabilities)
                        && probability(*confidence)
                }
                (
                    Question::Score { criteria, .. },
                    Some(Answer::Score {
                        score,
                        legend,
                        probabilities,
                        confidence,
                    }),
                ) => {
                    let keys: std::collections::BTreeSet<_> =
                        (0..criteria.len()).map(|i| i.to_string()).collect();
                    score.is_finite()
                        && *score >= 0.0
                        && *score <= (criteria.len() - 1) as f64
                        && keys.iter().eq(legend.keys())
                        && keys.iter().eq(probabilities.keys())
                        && distribution(probabilities)
                        && probability(*confidence)
                }
                _ => false,
            };
            if !valid {
                return Err("Response contains missing, mismatched or invalid typed answers");
            }
        }
        Ok(())
    }
}
fn read_bounded(reader: impl Read) -> io::Result<Vec<u8>> {
    let mut bytes = Vec::new();
    reader.take(MAX_BYTES + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "Input/response exceeds 1 MiB harness limit",
        ));
    }
    Ok(bytes)
}
fn run() -> Result<(), Box<dyn Error>> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    if args.is_empty() || args[0] == "--help" {
        println!("Jev typed-question harness\nUsage: jev-harness <request.json|-> [--dry-run]\nSet TYPESAFE_API_KEY in the process environment.\nDry-run validates and prints the request without credentials or network.\nOnly the explicit request is sent; no repository scanning. Responses go to stdout.\nLive requests disclose state to TypeSafe and may incur API charges.");
        return Ok(());
    }
    if args.len() > 2 || (args.len() == 2 && args[1] != "--dry-run") {
        return Err("Invalid arguments; use --help".into());
    }
    let bytes = if args[0] == "-" {
        read_bounded(io::stdin().lock())?
    } else {
        read_bounded(File::open(&args[0])?)?
    };
    let request: Request = serde_json::from_slice(&bytes)
        .map_err(|_| "Invalid request JSON/schema; use the documented examples")?;
    request.validate()?;
    if args.get(1).is_some_and(|arg| arg == "--dry-run") {
        println!("{}", serde_json::to_string_pretty(&request)?);
        return Ok(());
    }
    let key = std::env::var("TYPESAFE_API_KEY")
        .map_err(|_| "Set TYPESAFE_API_KEY to a rotated key before a live request")?;
    if key.trim().is_empty() {
        return Err("TYPESAFE_API_KEY is empty".into());
    }
    let client = reqwest::blocking::Client::builder()
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(60))
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    // No automatic retries: a retry could duplicate a billed inference.
    let response = client
        .post(ENDPOINT)
        .bearer_auth(key)
        .json(&request)
        .send()
        .map_err(|_| "TypeSafe request failed (connection/TLS/timeout); no automatic retry")?;
    let status = response.status();
    if !status.is_success() {
        // Never print server error bodies, which may echo submitted private state.
        return Err(format!(
            "TypeSafe HTTP {}; 401: key, 422: schema, 429/529: back off before retrying",
            status.as_u16()
        )
        .into());
    }
    let response: Response = serde_json::from_slice(&read_bounded(response)?)
        .map_err(|_| "Invalid TypeSafe response schema")?;
    request.validate_response(&response)?;
    println!("{}", serde_json::to_string_pretty(&response)?);
    Ok(())
}
fn main() {
    if let Err(error) = run() {
        eprintln!("{error}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn request() -> Request {
        serde_json::from_value(serde_json::json!({"state":"a claim and evidence", "questions":{"supported":{"type":"noul","instructions":"Does evidence support the claim?"}}})).unwrap()
    }
    #[test]
    fn default_model_and_roundtrip() {
        let r = request();
        r.validate().unwrap();
        assert_eq!(r.model, "jev-latest");
        let copy: Request = serde_json::from_slice(&serde_json::to_vec(&r).unwrap()).unwrap();
        copy.validate().unwrap();
    }
    #[test]
    fn malformed_questions_rejected() {
        let mut r = request();
        r.questions.clear();
        assert!(r.validate().is_err());
        r.questions.insert(
            "bad".into(),
            Question::Score {
                instructions: Value::String("Rate".into()),
                criteria: vec![],
            },
        );
        assert!(r.validate().is_err());
        assert!(serde_json::from_value::<Question>(
            serde_json::json!({"type":"invented","instructions":"x"})
        )
        .is_err());
    }
    #[test]
    fn response_types_and_probabilities_checked() {
        let r = request();
        let mut response = Response {
            model: "jev-1.13.0".into(),
            answers: BTreeMap::from([("supported".into(), Answer::Noul { noul: 0.7 })]),
            usage: Value::Null,
        };
        r.validate_response(&response).unwrap();
        response
            .answers
            .insert("supported".into(), Answer::Noul { noul: 1.1 });
        assert!(r.validate_response(&response).is_err());
        response.answers.clear();
        assert!(r.validate_response(&response).is_err());
    }
    #[test]
    fn bounds_and_distributions() {
        assert!(read_bounded(vec![0; MAX_BYTES as usize + 1].as_slice()).is_err());
        assert!(!distribution(&BTreeMap::from([("a".into(), 0.4)])));
        assert!(distribution(&BTreeMap::from([
            ("a".into(), 0.4),
            ("b".into(), 0.6)
        ])));
    }
}
