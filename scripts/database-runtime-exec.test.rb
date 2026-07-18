#!/usr/bin/env ruby

require "minitest/autorun"

require_relative "database-runtime-exec"

class DatabaseRuntimeExecTest < Minitest::Test
  PASSWORD = "a secure generated password with symbols !@#"

  def test_replaces_owner_url_and_removes_bootstrap_secrets
    binding = DatabaseRuntime.parse_binding(
      "DATABASE_OWNER_URL,robin_app_runtime,ROBIN_APP_RUNTIME_DATABASE_PASSWORD,DATABASE_URL"
    )
    environment = DatabaseRuntime.build_environment(
      [binding],
      {
        "DATABASE_OWNER_URL" => "postgresql://owner:owner-secret@db.internal:5432/robin?sslmode=require",
        "ROBIN_APP_RUNTIME_DATABASE_PASSWORD" => PASSWORD,
        "PORT" => "3000"
      }
    )

    refute environment.key?("DATABASE_OWNER_URL")
    refute environment.key?("ROBIN_APP_RUNTIME_DATABASE_PASSWORD")
    assert_equal "3000", environment.fetch("PORT")
    url = URI.parse(environment.fetch("DATABASE_URL"))
    assert_equal "robin_app_runtime", url.user
    assert_equal "db.internal", url.host
    assert_equal "/robin", url.path
    assert_equal "sslmode=require", url.query
    refute_includes environment.fetch("DATABASE_URL"), "owner-secret"
  end

  def test_supports_multiple_database_bindings
    bindings = [
      DatabaseRuntime.parse_binding(
        "RESEARCH_OWNER_URL,robin_research_runtime,RESEARCH_PASSWORD,DATABASE_URL"
      ),
      DatabaseRuntime.parse_binding(
        "APP_OWNER_URL,robin_app_readonly,APP_PASSWORD,AGENT_DATABASE_URL"
      )
    ]
    environment = DatabaseRuntime.build_environment(
      bindings,
      {
        "RESEARCH_OWNER_URL" => "postgres://owner:one@research.internal/research",
        "RESEARCH_PASSWORD" => PASSWORD,
        "APP_OWNER_URL" => "postgres://owner:two@app.internal/app",
        "APP_PASSWORD" => PASSWORD
      }
    )

    assert_equal "robin_research_runtime", URI.parse(environment.fetch("DATABASE_URL")).user
    assert_equal "robin_app_readonly", URI.parse(environment.fetch("AGENT_DATABASE_URL")).user
    refute environment.keys.any? { |key| key.end_with?("OWNER_URL", "PASSWORD") }
  end

  def test_removes_non_runtime_bootstrap_secret
    binding = DatabaseRuntime.parse_binding(
      "DATABASE_OWNER_URL,robin_lighter_provisioner,RUNTIME_PASSWORD,DATABASE_URL"
    )
    environment = DatabaseRuntime.build_environment(
      [binding],
      {
        "DATABASE_OWNER_URL" => "postgres://owner:secret@db.internal/lighter",
        "RUNTIME_PASSWORD" => PASSWORD,
        "READONLY_PASSWORD" => PASSWORD,
        "UNBOUND_DATABASE_OWNER_URL" => "postgres://owner:secret@other.internal/other",
        "UNBOUND_DATABASE_PASSWORD" => PASSWORD,
        "UNBOUND_DATABASE_URL" => "postgres://owner:secret@other.internal/other",
        "PGPASSWORD" => "owner-secret",
        "PGUSER" => "owner"
      },
      ["READONLY_PASSWORD"]
    )
    refute environment.key?("READONLY_PASSWORD")
    refute environment.keys.any? { |key| key.start_with?("UNBOUND_DATABASE_") }
    refute environment.key?("PGPASSWORD")
    refute environment.key?("PGUSER")
  end

  def test_rejects_short_passwords_and_runtime_owner_aliasing
    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.runtime_url("postgres://owner:secret@db.internal/app", "robin_app_runtime", "short")
    end
    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.runtime_url(
        "postgres://robin_app_runtime:secret@db.internal/app",
        "robin_app_runtime",
        PASSWORD
      )
    end
  end

  def test_rejects_malformed_binding
    assert_raises(DatabaseRuntime::Error) { DatabaseRuntime.parse_binding("DATABASE_URL,role") }
    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.parse_binding("DATABASE_URL,postgres,PASSWORD,TARGET")
    end
    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.parse_binding(
        "DATABASE_URL,robin_app_api,PASSWORD,DATABASE_URL"
      )
    end
  end

  def test_rejects_ambiguous_runtime_targets
    first = DatabaseRuntime.parse_binding(
      "APP_OWNER_URL,robin_app_api,APP_PASSWORD,DATABASE_URL"
    )
    second = DatabaseRuntime.parse_binding(
      "RESEARCH_OWNER_URL,robin_research_readonly,RESEARCH_PASSWORD,DATABASE_URL"
    )
    source = {
      "APP_OWNER_URL" => "postgres://owner:secret@app.internal/app",
      "APP_PASSWORD" => PASSWORD,
      "RESEARCH_OWNER_URL" => "postgres://owner:secret@research.internal/research",
      "RESEARCH_PASSWORD" => PASSWORD
    }

    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.build_environment([first, second], source)
    end
    assert_raises(DatabaseRuntime::Error) do
      DatabaseRuntime.build_environment([first], source, ["DATABASE_URL"])
    end
  end
end
