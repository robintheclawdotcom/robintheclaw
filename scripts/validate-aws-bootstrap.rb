#!/usr/bin/env ruby
# frozen_string_literal: true

require "digest"
require "json"
require "yaml"

path = ARGV.fetch(0, "ops/aws/render-kms-bootstrap.yaml")
template = YAML.safe_load(File.read(path), permitted_classes: [], aliases: false)

def check(condition, message)
  raise message unless condition
end

def sub(value)
  { "Fn::Sub" => value }
end

def get_att(resource, attribute)
  { "Fn::GetAtt" => [resource, attribute] }
end

def policy(role, name)
  properties = role.fetch("Properties")
  check(!properties.key?("ManagedPolicyArns"), "#{name} must not use managed policies")
  check(properties.fetch("Policies").length == 1, "#{name} must have one inline policy")
  properties.fetch("Policies").first.fetch("PolicyDocument")
end

def statements(role, name)
  document = policy(role, name)
  check(document["Version"] == "2012-10-17", "#{name} has the wrong policy version")
  entries = document.fetch("Statement")
  check(entries.all? { |entry| entry["Sid"] && entry["Effect"] && entry["Action"] && entry["Resource"] }, "#{name} has an incomplete statement")
  check(entries.map { |entry| entry["Sid"] }.uniq.length == entries.length, "#{name} has duplicate statement IDs")
  entries.to_h { |entry| [entry["Sid"], entry] }
end

def oidc_trust(role, name, workspace:, environment:, service:, policies:)
  properties = role.fetch("Properties")
  expected_keys = %w[AssumeRolePolicyDocument MaxSessionDuration Tags]
  expected_keys << "Policies" if policies
  check(properties.keys.sort == expected_keys.sort, "#{name} properties changed")
  check(properties["MaxSessionDuration"] == 3600, "#{name} session duration changed")
  trust = properties.dig("AssumeRolePolicyDocument", "Fn::Sub")
  check(trust.is_a?(Array) && trust.length == 2, "#{name} trust is not a fixed substitution")
  check(trust[1] == { "ProviderArn" => { "Ref" => "RenderOIDCProvider" } }, "#{name} trusts the wrong provider")
  parsed = JSON.parse(trust[0])
  expected = {
    "Version" => "2012-10-17",
    "Statement" => [{
      "Effect" => "Allow",
      "Principal" => { "Federated" => "${ProviderArn}" },
      "Action" => "sts:AssumeRoleWithWebIdentity",
      "Condition" => {
        "StringEquals" => {
          "oidc.render.com/#{workspace}:aud" => "sts.amazonaws.com",
          "oidc.render.com/#{workspace}:sub" => "workspace:#{workspace}:environment:#{environment}:service:#{service}"
        }
      }
    }]
  }
  check(parsed == expected, "#{name} trust policy changed")
end

parameters = template.fetch("Parameters")
resources = template.fetch("Resources")
outputs = template.fetch("Outputs")

expected_parameters = %w[
  RenderWorkspaceId
  RenderEnvironmentId
  LighterProvisionerServiceId
  RobinhoodProvisionerServiceId
  RobinhoodSignerServiceId
]
check(parameters.keys.sort == expected_parameters.sort, "unexpected AWS parameter set")
check(parameters.dig("RenderWorkspaceId", "AllowedPattern") == "^tea-[a-z0-9]+$", "workspace parameter is not exact")
check(parameters.dig("RenderEnvironmentId", "AllowedPattern") == "^evm-[a-z0-9]+$", "environment parameter is not exact")
%w[LighterProvisionerServiceId RobinhoodProvisionerServiceId RobinhoodSignerServiceId].each do |name|
  check(parameters.dig(name, "AllowedPattern") == "^srv-[a-z0-9]+$", "#{name} is not exact")
end

expected_resources = %w[
  RenderOIDCProvider
  LighterEnvelopeKey
  LighterEnvelopeAlias
  LighterProvisionerRole
  RobinhoodKeyLedger
  RobinhoodKeyControlPlaneRole
  RobinhoodKeyControlPlaneLogGroup
  RobinhoodKeyControlPlane
  RobinhoodKeyControlPlaneVersion
  RobinhoodProvisionerRole
  RobinhoodSignerRole
]
check(resources.keys.sort == expected_resources.sort, "unexpected AWS resource set")

provider = resources.fetch("RenderOIDCProvider")
check(provider == {
  "Type" => "AWS::IAM::OIDCProvider",
  "Properties" => {
    "Url" => sub("https://oidc.render.com/${RenderWorkspaceId}"),
    "ClientIdList" => ["sts.amazonaws.com"],
    "Tags" => [{ "Key" => "service", "Value" => "robin-mainnet" }]
  }
}, "Render OIDC provider changed")
check(!provider.fetch("Properties").key?("ThumbprintList"), "OIDC thumbprints must be discovered by IAM")

lighter_key = resources.fetch("LighterEnvelopeKey")
check(lighter_key["Type"] == "AWS::KMS::Key", "Lighter key type changed")
check(lighter_key["DeletionPolicy"] == "Retain" && lighter_key["UpdateReplacePolicy"] == "Retain", "Lighter key is not retained")
check(lighter_key.fetch("Properties").slice("Description", "EnableKeyRotation", "KeySpec", "KeyUsage", "MultiRegion", "PendingWindowInDays", "Tags") == {
  "Description" => "Lighter credential envelope key",
  "EnableKeyRotation" => true,
  "KeySpec" => "SYMMETRIC_DEFAULT",
  "KeyUsage" => "ENCRYPT_DECRYPT",
  "MultiRegion" => false,
  "PendingWindowInDays" => 30,
  "Tags" => [{ "Key" => "service", "Value" => "lighter-provisioner" }]
}, "Lighter key properties changed")

lighter_alias = resources.fetch("LighterEnvelopeAlias")
check(lighter_alias == {
  "Type" => "AWS::KMS::Alias",
  "DeletionPolicy" => "Retain",
  "UpdateReplacePolicy" => "Retain",
  "Properties" => {
    "AliasName" => "alias/robin/lighter/credentials",
    "TargetKeyId" => { "Ref" => "LighterEnvelopeKey" }
  }
}, "Lighter envelope alias is not fixed and retained")

oidc_roles = {
  "LighterProvisionerRole" => ["${LighterProvisionerServiceId}", true],
  "RobinhoodProvisionerRole" => ["${RobinhoodProvisionerServiceId}", true],
  "RobinhoodSignerRole" => ["${RobinhoodSignerServiceId}", false]
}
oidc_roles.each do |name, (service, policies)|
  oidc_trust(
    resources.fetch(name),
    name,
    workspace: "${RenderWorkspaceId}",
    environment: "${RenderEnvironmentId}",
    service: service,
    policies: policies
  )
end

lighter = statements(resources.fetch("LighterProvisionerRole"), "LighterProvisionerRole")
check(lighter.keys == ["UseLighterEnvelopeKey"], "Lighter role statement set changed")
check(lighter["UseLighterEnvelopeKey"] == {
  "Sid" => "UseLighterEnvelopeKey",
  "Effect" => "Allow",
  "Action" => %w[kms:Decrypt kms:GenerateDataKey],
  "Resource" => get_att("LighterEnvelopeKey", "Arn"),
  "Condition" => {
    "StringEquals" => {
      "kms:EncryptionAlgorithm" => "SYMMETRIC_DEFAULT",
      "kms:EncryptionContext:service" => "lighter-provisioner",
      "kms:RequestAlias" => "alias/robin/lighter/credentials"
    },
    "StringLike" => {
      "kms:EncryptionContext:executionAccountId" => "????????-????-????-????-????????????",
      "kms:EncryptionContext:accountIndex" => "?*",
      "kms:EncryptionContext:apiKeyIndex" => "?*",
      "kms:EncryptionContext:credentialVersion" => "?*"
    },
    "ForAllValues:StringEquals" => {
      "kms:EncryptionContextKeys" => %w[service executionAccountId accountIndex apiKeyIndex credentialVersion]
    }
  }
}, "Lighter envelope policy changed")

ledger = resources.fetch("RobinhoodKeyLedger")
check(ledger["Type"] == "AWS::DynamoDB::Table", "Robinhood key ledger type changed")
check(ledger["DeletionPolicy"] == "Retain" && ledger["UpdateReplacePolicy"] == "Retain", "Robinhood key ledger is not retained")
check(ledger.fetch("Properties") == {
  "BillingMode" => "PAY_PER_REQUEST",
  "AttributeDefinitions" => [{ "AttributeName" => "executionAccountId", "AttributeType" => "S" }],
  "KeySchema" => [{ "AttributeName" => "executionAccountId", "KeyType" => "HASH" }],
  "PointInTimeRecoverySpecification" => { "PointInTimeRecoveryEnabled" => true },
  "SSESpecification" => { "SSEEnabled" => true },
  "Tags" => [{ "Key" => "service", "Value" => "robinhood-key-control-plane" }]
}, "Robinhood key ledger changed")

control_role = resources.fetch("RobinhoodKeyControlPlaneRole")
control_properties = control_role.fetch("Properties")
check(control_properties.keys.sort == %w[AssumeRolePolicyDocument MaxSessionDuration Policies Tags].sort, "control-plane role properties changed")
check(control_properties["MaxSessionDuration"] == 3600, "control-plane session duration changed")
check(control_properties["AssumeRolePolicyDocument"] == {
  "Version" => "2012-10-17",
  "Statement" => [{
    "Effect" => "Allow",
    "Principal" => { "Service" => "lambda.amazonaws.com" },
    "Action" => "sts:AssumeRole"
  }]
}, "control-plane trust policy changed")

control = statements(control_role, "RobinhoodKeyControlPlaneRole")
check(control.keys.sort == %w[
  WriteFunctionLogs
  UseProvisioningLedger
  CreateFixedExecutionKeys
  CreateExecutionAliases
  ListExecutionAliases
  DenyKeyEscalationAndSigning
].sort, "control-plane statement set changed")

key_arn = sub("arn:${AWS::Partition}:kms:${AWS::Region}:${AWS::AccountId}:key/*")
alias_arn = sub("arn:${AWS::Partition}:kms:${AWS::Region}:${AWS::AccountId}:alias/robinhood/execution/*")
fixed_key_conditions = {
  "StringEquals" => {
    "kms:KeyOrigin" => "AWS_KMS",
    "kms:KeySpec" => "ECC_SECG_P256K1",
    "kms:KeyUsage" => "SIGN_VERIFY"
  },
  "Bool" => {
    "kms:BypassPolicyLockoutSafetyCheck" => "true",
    "kms:MultiRegion" => "false"
  }
}
check(control["WriteFunctionLogs"] == {
  "Sid" => "WriteFunctionLogs",
  "Effect" => "Allow",
  "Action" => %w[logs:CreateLogStream logs:PutLogEvents],
  "Resource" => sub("arn:${AWS::Partition}:logs:${AWS::Region}:${AWS::AccountId}:log-group:/aws/lambda/robinhood-execution-key-control-plane:*")
}, "control-plane logging policy changed")
check(control["UseProvisioningLedger"] == {
  "Sid" => "UseProvisioningLedger",
  "Effect" => "Allow",
  "Action" => %w[dynamodb:GetItem dynamodb:PutItem dynamodb:UpdateItem],
  "Resource" => get_att("RobinhoodKeyLedger", "Arn")
}, "control-plane ledger policy changed")
check(control["CreateFixedExecutionKeys"] == {
  "Sid" => "CreateFixedExecutionKeys",
  "Effect" => "Allow",
  "Action" => "kms:CreateKey",
  "Resource" => "*",
  "Condition" => fixed_key_conditions
}, "control-plane CreateKey policy changed")
check(control["CreateExecutionAliases"] == {
  "Sid" => "CreateExecutionAliases",
  "Effect" => "Allow",
  "Action" => "kms:CreateAlias",
  "Resource" => alias_arn
}, "control-plane alias policy changed")
check(control["ListExecutionAliases"] == {
  "Sid" => "ListExecutionAliases",
  "Effect" => "Allow",
  "Action" => "kms:ListAliases",
  "Resource" => "*"
}, "control-plane alias inspection policy changed")
check(control["DenyKeyEscalationAndSigning"] == {
  "Sid" => "DenyKeyEscalationAndSigning",
  "Effect" => "Deny",
  "Action" => %w[kms:CreateGrant kms:PutKeyPolicy kms:Sign kms:TagResource kms:UntagResource],
  "Resource" => key_arn
}, "control-plane escalation deny changed")

provisioner = statements(resources.fetch("RobinhoodProvisionerRole"), "RobinhoodProvisionerRole")
check(provisioner == {
  "InvokeFixedKeyControlPlane" => {
    "Sid" => "InvokeFixedKeyControlPlane",
    "Effect" => "Allow",
    "Action" => "lambda:InvokeFunction",
    "Resource" => { "Ref" => "RobinhoodKeyControlPlaneVersion" }
  }
}, "Robinhood provisioner must invoke only the fixed key control plane")
provisioner_actions = provisioner.values.flat_map { |entry| Array(entry["Action"]) }
check((provisioner_actions & %w[kms:CreateKey kms:TagResource kms:CreateAlias kms:UpdateAlias kms:Sign]).empty?, "Robinhood provisioner has KMS authority")

signer_role = resources.fetch("RobinhoodSignerRole")
check(!signer_role.fetch("Properties").key?("Policies"), "signer role must not have identity-based KMS allows")

function = resources.fetch("RobinhoodKeyControlPlane")
function_properties = function.fetch("Properties")
check(function["Type"] == "AWS::Lambda::Function", "control-plane function type changed")
check(function_properties.keys.sort == %w[Code Environment FunctionName Handler MemorySize ReservedConcurrentExecutions Role Runtime Tags Timeout].sort, "control-plane function properties changed")
check(function_properties.slice("Runtime", "Handler", "FunctionName", "Timeout", "MemorySize", "ReservedConcurrentExecutions", "Role", "Environment", "Tags") == {
  "Runtime" => "python3.12",
  "Handler" => "index.handler",
  "FunctionName" => "robinhood-execution-key-control-plane",
  "Timeout" => 30,
  "MemorySize" => 256,
  "ReservedConcurrentExecutions" => 1,
  "Role" => get_att("RobinhoodKeyControlPlaneRole", "Arn"),
  "Environment" => {
    "Variables" => {
      "ROBIN_ACCOUNT_ID" => { "Ref" => "AWS::AccountId" },
      "ROBIN_PARTITION" => { "Ref" => "AWS::Partition" },
      "ROBIN_KEY_LEDGER" => { "Ref" => "RobinhoodKeyLedger" },
      "ROBIN_CONTROL_ROLE_ARN" => get_att("RobinhoodKeyControlPlaneRole", "Arn"),
      "ROBIN_SIGNER_ROLE_ARN" => get_att("RobinhoodSignerRole", "Arn")
    }
  },
  "Tags" => [{ "Key" => "service", "Value" => "robinhood-key-control-plane" }]
}, "control-plane function configuration changed")
code = function_properties.dig("Code", "ZipFile")
check(code.is_a?(String), "control-plane function must use reviewed inline code")
expected_code_sha256 = "cdfe17597901fd48781d002724f2a82aa7dd4f83e7e3a09480fed58ebab4581a"
check(Digest::SHA256.hexdigest(code) == expected_code_sha256, "control-plane function code changed")

function_version = resources.fetch("RobinhoodKeyControlPlaneVersion")
check(function_version == {
  "Type" => "AWS::Lambda::Version",
  "DeletionPolicy" => "Retain",
  "UpdateReplacePolicy" => "Retain",
  "Properties" => {
    "Description" => "reviewed-source-sha256-#{expected_code_sha256}",
    "FunctionName" => { "Ref" => "RobinhoodKeyControlPlane" }
  }
}, "control-plane function version is not immutable and source-pinned")

log_group = resources.fetch("RobinhoodKeyControlPlaneLogGroup")
check(log_group == {
  "Type" => "AWS::Logs::LogGroup",
  "DeletionPolicy" => "Retain",
  "UpdateReplacePolicy" => "Retain",
  "Properties" => {
    "LogGroupName" => "/aws/lambda/robinhood-execution-key-control-plane",
    "RetentionInDays" => 90
  }
}, "control-plane log group changed")

expected_outputs = {
  "LighterProvisionerRoleArn" => { "Value" => get_att("LighterProvisionerRole", "Arn") },
  "LighterEnvelopeKeyAlias" => { "Value" => "alias/robin/lighter/credentials" },
  "RobinhoodKeyControlPlaneArn" => { "Value" => { "Ref" => "RobinhoodKeyControlPlaneVersion" } },
  "RobinhoodProvisionerRoleArn" => { "Value" => get_att("RobinhoodProvisionerRole", "Arn") },
  "RobinhoodSignerRoleArn" => { "Value" => get_att("RobinhoodSignerRole", "Arn") }
}
check(outputs == expected_outputs, "bootstrap outputs changed")

role_statements = %w[
  LighterProvisionerRole
  RobinhoodKeyControlPlaneRole
  RobinhoodProvisionerRole
].flat_map do |name|
  statements(resources.fetch(name), name).values
end
role_actions = role_statements.flat_map { |entry| Array(entry["Action"]) }
allowed_role_actions = role_statements
  .select { |entry| entry["Effect"] == "Allow" }
  .flat_map { |entry| Array(entry["Action"]) }
check(role_actions.none? { |action| action.end_with?("*") }, "wildcard IAM action found")
check((allowed_role_actions & %w[kms:CreateGrant kms:DeleteAlias kms:DisableKey kms:Encrypt kms:PutKeyPolicy kms:ScheduleKeyDeletion kms:Sign kms:TagResource kms:UntagResource kms:UpdateAlias]).empty?, "destructive or unrelated KMS allow found")

text = File.read(path)
check(!text.match?(%r{/Users/|/home/|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}}), "template contains local identity or credentials")

puts "aws bootstrap template valid"
