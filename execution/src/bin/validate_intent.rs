use execution::PairIntent;
use std::{env, fs, process};

fn main() {
    let args = env::args().skip(1).collect::<Vec<_>>();
    let path = match args.as_slice() {
        [path] => path,
        _ => {
            eprintln!("usage: validate-intent <intent.json>");
            process::exit(2);
        }
    };
    let value = fs::read_to_string(path).unwrap_or_else(|error| {
        eprintln!("failed to read input: {error}");
        process::exit(2);
    });
    let intent: PairIntent = serde_json::from_str(&value).unwrap_or_else(|error| {
        eprintln!("invalid input: {error}");
        process::exit(2);
    });
    intent.validate().unwrap_or_else(|error| {
        eprintln!("intent declined: {error}");
        process::exit(1);
    });
    println!("intent is valid");
}
