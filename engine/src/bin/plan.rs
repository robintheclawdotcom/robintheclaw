use std::{env, fs, process};

use engine::{decide, DecisionInput};

fn main() {
    let args = env::args().skip(1).collect::<Vec<_>>();
    let path = match args.as_slice() {
        [path] => path,
        _ => {
            eprintln!("usage: plan <input.json>");
            process::exit(2);
        }
    };

    let input = fs::read_to_string(path).unwrap_or_else(|error| {
        eprintln!("failed to read {path}: {error}");
        process::exit(2);
    });
    let input: DecisionInput = serde_json::from_str(&input).unwrap_or_else(|error| {
        eprintln!("invalid decision input: {error}");
        process::exit(2);
    });
    let decision = decide(&input);
    println!(
        "{}",
        serde_json::to_string_pretty(&decision).expect("decision is serializable")
    );
}
