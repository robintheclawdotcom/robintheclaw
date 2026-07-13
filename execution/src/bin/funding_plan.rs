use execution::{FundingInput, FundingPlan};
use std::{env, fs, process};

fn main() {
    let path = input_path("funding-plan");
    let input: FundingInput = read_json(&path);
    let plan = FundingPlan::calculate(&input).unwrap_or_else(|| {
        eprintln!("invalid funding input");
        process::exit(1);
    });
    println!("{}", serde_json::to_string_pretty(&plan).unwrap());
}

fn input_path(command: &str) -> String {
    let args = env::args().skip(1).collect::<Vec<_>>();
    match args.as_slice() {
        [path] => path.clone(),
        _ => {
            eprintln!("usage: {command} <input.json>");
            process::exit(2);
        }
    }
}

fn read_json<T: serde::de::DeserializeOwned>(path: &str) -> T {
    let value = fs::read_to_string(path).unwrap_or_else(|error| {
        eprintln!("failed to read input: {error}");
        process::exit(2);
    });
    serde_json::from_str(&value).unwrap_or_else(|error| {
        eprintln!("invalid input: {error}");
        process::exit(2);
    })
}
