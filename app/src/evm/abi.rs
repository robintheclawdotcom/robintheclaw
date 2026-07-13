use crate::product_store::normalize_address;
use anyhow::{anyhow, Result};
use num_bigint::BigUint;
use sha3::{Digest, Keccak256};

pub fn selector(signature: &str) -> [u8; 4] {
    let digest = Keccak256::digest(signature.as_bytes());
    [digest[0], digest[1], digest[2], digest[3]]
}

pub fn call_no_args(signature: &str) -> String {
    format!("0x{}", hex::encode(selector(signature)))
}

pub fn call_address(signature: &str, address: &str) -> Result<String> {
    Ok(format!(
        "0x{}{}",
        hex::encode(selector(signature)),
        encode_address_word(address)?
    ))
}

pub fn call_u256(signature: &str, value: &str) -> Result<String> {
    Ok(format!(
        "0x{}{}",
        hex::encode(selector(signature)),
        encode_u256_word(value)?
    ))
}

pub fn call_address_u256(signature: &str, address: &str, value: &str) -> Result<String> {
    Ok(format!(
        "0x{}{}{}",
        hex::encode(selector(signature)),
        encode_address_word(address)?,
        encode_u256_word(value)?
    ))
}

pub fn encode_address_word(address: &str) -> Result<String> {
    let normalized = normalize_address(address)?;
    Ok(format!("{:0>64}", normalized[2..].to_ascii_lowercase()))
}

pub fn encode_u256_word(value: &str) -> Result<String> {
    let value = BigUint::parse_bytes(value.as_bytes(), 10)
        .ok_or_else(|| anyhow!("invalid integer amount"))?;
    let encoded = value.to_str_radix(16);
    if encoded.len() > 64 {
        return Err(anyhow!("integer amount exceeds uint256"));
    }
    Ok(format!("{encoded:0>64}"))
}

pub fn decode_address(value: &str) -> Result<String> {
    let word = first_word(value)?;
    normalize_address(&format!("0x{}", &word[24..]))
}

pub fn decode_bool(value: &str) -> Result<bool> {
    let word = first_word(value)?;
    match BigUint::parse_bytes(word.as_bytes(), 16) {
        Some(value) if value == BigUint::from(0_u8) => Ok(false),
        Some(value) if value == BigUint::from(1_u8) => Ok(true),
        _ => Err(anyhow!("invalid ABI boolean")),
    }
}

pub fn decode_u256(value: &str) -> Result<String> {
    let word = first_word(value)?;
    BigUint::parse_bytes(word.as_bytes(), 16)
        .map(|value| value.to_str_radix(10))
        .ok_or_else(|| anyhow!("invalid ABI uint256"))
}

pub fn sum_decimal(left: &str, right: &str) -> Result<String> {
    let left = BigUint::parse_bytes(left.as_bytes(), 10)
        .ok_or_else(|| anyhow!("invalid decimal integer"))?;
    let right = BigUint::parse_bytes(right.as_bytes(), 10)
        .ok_or_else(|| anyhow!("invalid decimal integer"))?;
    Ok((left + right).to_str_radix(10))
}

pub fn word(value: &str, index: usize) -> Result<&str> {
    let encoded = value.strip_prefix("0x").unwrap_or(value);
    let start = index
        .checked_mul(64)
        .ok_or_else(|| anyhow!("ABI word index overflow"))?;
    let end = start + 64;
    encoded
        .get(start..end)
        .ok_or_else(|| anyhow!("missing ABI word"))
}

fn first_word(value: &str) -> Result<&str> {
    word(value, 0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encodes_approve_call() {
        let call = call_address_u256(
            "approve(address,uint256)",
            "0x1111111111111111111111111111111111111111",
            "1000",
        )
        .unwrap();
        assert!(call.starts_with("0x095ea7b3"));
        assert_eq!(call.len(), 2 + 8 + 64 + 64);
    }

    #[test]
    fn decodes_abi_values() {
        assert!(decode_bool(&format!("0x{:0>64}", "1")).unwrap());
        assert_eq!(decode_u256(&format!("0x{:0>64}", "3e8")).unwrap(), "1000");
        assert_eq!(
            decode_address(&format!(
                "0x{:0>64}",
                "1111111111111111111111111111111111111111"
            ))
            .unwrap(),
            "0x1111111111111111111111111111111111111111"
        );
    }
}
