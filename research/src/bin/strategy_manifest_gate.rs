use research::StrategyManifest;
use std::{env, fs, process};

fn main() {
    let args = env::args().skip(1).collect::<Vec<_>>();
    let path = match args.as_slice() {
        [path] => path,
        _ => {
            eprintln!("usage: strategy-manifest-gate <manifest.json>");
            process::exit(2);
        }
    };
    let value = fs::read_to_string(path).unwrap_or_else(|error| {
        eprintln!("failed to read strategy manifest: {error}");
        process::exit(2);
    });
    let manifest: StrategyManifest = serde_json::from_str(&value).unwrap_or_else(|error| {
        eprintln!("invalid strategy manifest: {error}");
        process::exit(2);
    });
    manifest.validate().unwrap_or_else(|error| {
        eprintln!("strategy manifest rejected: {error}");
        process::exit(1);
    });
    println!("strategy manifest is valid: {}", manifest.sha256);
}
