use crate::auth::AuthError;
use actix_web::{http::StatusCode, HttpResponse, ResponseError};
use serde_json::json;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum ApiError {
    #[error("authentication required")]
    Unauthorized,
    #[error("authentication service is not configured")]
    AuthUnavailable,
    #[error("{0}")]
    BadRequest(String),
    #[error("{0}")]
    Conflict(String),
    #[error("{0}")]
    ServiceUnavailable(String),
    #[error("internal service error")]
    Internal,
}

impl ApiError {
    pub fn internal(error: impl std::fmt::Display) -> Self {
        log::error!("application API error: {error}");
        Self::Internal
    }
}

impl From<AuthError> for ApiError {
    fn from(error: AuthError) -> Self {
        match error {
            AuthError::Missing | AuthError::Invalid => Self::Unauthorized,
            AuthError::Unavailable => Self::AuthUnavailable,
        }
    }
}

impl ResponseError for ApiError {
    fn status_code(&self) -> StatusCode {
        match self {
            Self::Unauthorized => StatusCode::UNAUTHORIZED,
            Self::AuthUnavailable | Self::ServiceUnavailable(_) => StatusCode::SERVICE_UNAVAILABLE,
            Self::BadRequest(_) => StatusCode::BAD_REQUEST,
            Self::Conflict(_) => StatusCode::CONFLICT,
            Self::Internal => StatusCode::INTERNAL_SERVER_ERROR,
        }
    }

    fn error_response(&self) -> HttpResponse {
        let code = match self {
            Self::Unauthorized => "unauthorized",
            Self::AuthUnavailable => "auth_unavailable",
            Self::BadRequest(_) => "invalid_request",
            Self::Conflict(_) => "conflict",
            Self::ServiceUnavailable(_) => "service_unavailable",
            Self::Internal => "internal_error",
        };
        HttpResponse::build(self.status_code()).json(json!({
            "error": code,
            "message": self.to_string(),
        }))
    }
}
