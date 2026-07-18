#!/usr/bin/env ruby

require "minitest/autorun"
require "open3"
require "uri"

class PsqlWithUrlTest < Minitest::Test
  SCRIPT = File.expand_path("psql-with-url.rb", __dir__)

  def test_rejects_missing_url_without_echoing_a_secret
    _stdout, stderr, status = Open3.capture3({}, "ruby", SCRIPT, "-c", "select 1")
    refute status.success?
    assert_equal "ROBIN_DATABASE_URL is required\n", stderr
  end

  def test_rejects_unsupported_connection_options
    url = "postgresql://owner:#{URI.encode_www_form_component("secret value")}@db.internal/app?options=unsafe"
    _stdout, stderr, status = Open3.capture3(
      { "ROBIN_DATABASE_URL" => url },
      "ruby",
      SCRIPT,
      "-c",
      "select 1"
    )
    refute status.success?
    assert_equal "ROBIN_DATABASE_URL contains unsupported options\n", stderr
    refute_includes stderr, "secret"
  end

  def test_rejects_invalid_urls_without_echoing_credentials
    [
      "postgresql://owner:@db.internal/app",
      "postgresql://owner:secret value@db.internal/app",
      "postgresql://owner:secret@db.internal/app#fragment"
    ].each do |url|
      _stdout, stderr, status = Open3.capture3(
        { "ROBIN_DATABASE_URL" => url },
        "ruby",
        SCRIPT,
        "-c",
        "select 1"
      )
      refute status.success?
      assert_equal "ROBIN_DATABASE_URL is invalid\n", stderr
      refute_includes stderr, "secret"
    end
  end
end
