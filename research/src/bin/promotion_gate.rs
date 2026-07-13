use research::PromotionEvidence;
use std::{env, fs, process};

fn main() {
    let args = env::args().skip(1).collect::<Vec<_>>();
    let path = match args.as_slice() {
        [path] => path,
        _ => {
            eprintln!("usage: promotion-gate <evidence.json>");
            process::exit(2);
        }
    };
    let value = fs::read_to_string(path).unwrap_or_else(|error| {
        eprintln!("failed to read evidence: {error}");
        process::exit(2);
    });
    let evidence: PromotionEvidence = serde_json::from_str(&value).unwrap_or_else(|error| {
        eprintln!("invalid evidence: {error}");
        process::exit(2);
    });
    let failures = evidence.canary_failures();
    if !failures.is_empty() {
        println!("{}", serde_json::to_string_pretty(&failures).unwrap());
        process::exit(1);
    }
    println!("strategy is canary eligible");
}
