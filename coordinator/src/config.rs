use std::{env, net::SocketAddr, str::FromStr};
use thiserror::Error;

#[derive(Debug, Clone)]
pub struct Config {
    pub enabled: bool,
    pub listen: SocketAddr,
    pub database_url: Option<String>,
    pub api_token: Option<String>,
    pub lighter_signer_url: Option<String>,
    pub robinhood_signer_url: Option<String>,
}

#[derive(Debug, Error, PartialEq, Eq)]
pub enum ConfigError {
    #[error("LISTEN_ADDRESS is invalid")]
    InvalidListen,
    #[error("{0} is required when execution is enabled")]
    Missing(&'static str),
    #[error("COORDINATOR_API_TOKEN must contain at least 32 bytes")]
    WeakToken,
    #[error("signer URLs must use HTTPS or Render private-network HTTP")]
    InvalidSignerUrl,
}

impl Config {
    pub fn from_env() -> Result<Self, ConfigError> {
        let enabled =
            env::var("COORDINATOR_ENABLED").is_ok_and(|value| value.eq_ignore_ascii_case("true"));
        let listen = env::var("LISTEN_ADDRESS")
            .unwrap_or_else(|_| "0.0.0.0:8080".into())
            .parse()
            .map_err(|_| ConfigError::InvalidListen)?;
        let mut config = Self {
            enabled,
            listen,
            database_url: env::var("DATABASE_URL").ok(),
            api_token: env::var("COORDINATOR_API_TOKEN").ok(),
            lighter_signer_url: env::var("LIGHTER_SIGNER_URL").ok(),
            robinhood_signer_url: env::var("ROBINHOOD_SIGNER_URL").ok(),
        };
        config.validate()?;
        if !enabled {
            config.database_url = None;
            config.api_token = None;
            config.lighter_signer_url = None;
            config.robinhood_signer_url = None;
        }
        Ok(config)
    }

    pub fn validate(&self) -> Result<(), ConfigError> {
        if !self.enabled {
            return Ok(());
        }
        let required = [
            (self.database_url.as_deref(), "DATABASE_URL"),
            (self.api_token.as_deref(), "COORDINATOR_API_TOKEN"),
            (self.lighter_signer_url.as_deref(), "LIGHTER_SIGNER_URL"),
            (self.robinhood_signer_url.as_deref(), "ROBINHOOD_SIGNER_URL"),
        ];
        for (value, name) in required {
            if value.is_none_or(str::is_empty) {
                return Err(ConfigError::Missing(name));
            }
        }
        if self
            .api_token
            .as_deref()
            .is_none_or(|value| value.len() < 32)
        {
            return Err(ConfigError::WeakToken);
        }
        for url in [
            self.lighter_signer_url.as_deref().unwrap(),
            self.robinhood_signer_url.as_deref().unwrap(),
        ] {
            if !(url.starts_with("https://") || is_private_http(url)) {
                return Err(ConfigError::InvalidSignerUrl);
            }
        }
        Ok(())
    }
}

fn is_private_http(value: &str) -> bool {
    let Some(authority) = value.strip_prefix("http://") else {
        return false;
    };
    let host = authority.split(['/', ':']).next().unwrap_or_default();
    host == "localhost"
        || host == "127.0.0.1"
        || host.ends_with(".internal")
        || std::net::IpAddr::from_str(host).is_ok_and(|address| match address {
            std::net::IpAddr::V4(address) => address.is_private() || address.is_loopback(),
            std::net::IpAddr::V6(address) => address.is_loopback() || address.is_unique_local(),
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn enabled() -> Config {
        Config {
            enabled: true,
            listen: "127.0.0.1:8080".parse().unwrap(),
            database_url: Some("postgres://db".into()),
            api_token: Some("a".repeat(32)),
            lighter_signer_url: Some("http://lighter.internal:8080".into()),
            robinhood_signer_url: Some("https://signer.example".into()),
        }
    }

    #[test]
    fn disabled_configuration_has_no_dependencies() {
        let mut config = enabled();
        config.enabled = false;
        config.database_url = None;
        config.api_token = None;
        config.lighter_signer_url = None;
        config.robinhood_signer_url = None;
        assert_eq!(config.validate(), Ok(()));
    }

    #[test]
    fn public_plaintext_signer_is_rejected() {
        let mut config = enabled();
        config.lighter_signer_url = Some("http://public.example".into());
        assert_eq!(config.validate(), Err(ConfigError::InvalidSignerUrl));
    }
}
