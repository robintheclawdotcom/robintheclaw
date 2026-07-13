use crate::intent::PairIntent;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "command", rename_all = "snake_case")]
pub enum LighterCommand {
    CreateOrder {
        intent_id: String,
        market_index: u32,
        client_order_index: u64,
        base_amount: u64,
        price: u32,
        immediate_or_cancel: bool,
        reduce_only: bool,
    },
    ModifyOrder {
        intent_id: String,
        order_index: u64,
        base_amount: u64,
        price: u32,
    },
    CancelOrder {
        intent_id: String,
        order_index: u64,
    },
    CancelAll {
        intent_id: String,
        market_index: Option<u32>,
    },
    ScheduleCancelAll {
        intent_id: String,
        execute_at_ms: u64,
    },
    CreateAuthToken {
        expires_at_ms: u64,
    },
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "command", rename_all = "snake_case")]
pub enum RobinhoodCommand {
    ExecuteSpot { intent: Box<PairIntent> },
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lighter_surface_has_no_asset_transfer_variant() {
        let commands = [
            LighterCommand::CancelAll {
                intent_id: "intent".into(),
                market_index: None,
            },
            LighterCommand::ScheduleCancelAll {
                intent_id: "intent".into(),
                execute_at_ms: 1,
            },
        ];
        for command in commands {
            let rendered = format!("{command:?}").to_ascii_lowercase();
            assert!(!rendered.contains("withdraw"));
            assert!(!rendered.contains("transfer"));
        }
    }

    #[test]
    fn robinhood_surface_contains_only_typed_spot_execution() {
        for command in ["halt", "recover", "transfer", "set_agent", "execute"] {
            let payload = format!(r#"{{"command":"{command}"}}"#);
            assert!(serde_json::from_str::<RobinhoodCommand>(&payload).is_err());
        }
    }
}
