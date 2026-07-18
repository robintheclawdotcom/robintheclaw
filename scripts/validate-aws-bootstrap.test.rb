#!/usr/bin/env ruby
# frozen_string_literal: true

require "open3"
require "tmpdir"
require "yaml"

root = File.expand_path("..", __dir__)
validator = File.join(root, "scripts", "validate-aws-bootstrap.rb")
template_path = File.join(root, "ops", "aws", "render-kms-bootstrap.yaml")
template = File.read(template_path)

def run_validator(validator, template)
  Open3.capture3("ruby", validator, template)
end

stdout, stderr, status = run_validator(validator, template_path)
raise "#{stdout}#{stderr}" unless status.success? && stdout.include?("template valid")

mutations = {
  "wildcard trust" => [
    "service:${LighterProvisionerServiceId}",
    "service:*"
  ],
  "extra trust statement" => [
    '"Statement":[{',
    '"Statement":[{}, {'
  ],
  "managed policy" => [
    "  RobinhoodProvisionerRole:\n    Type: AWS::IAM::Role\n    Properties:\n      MaxSessionDuration: 3600",
    "  RobinhoodProvisionerRole:\n    Type: AWS::IAM::Role\n    Properties:\n      ManagedPolicyArns:\n        - arn:aws:iam::aws:policy/AWSKeyManagementServicePowerUser\n      MaxSessionDuration: 3600"
  ],
  "wildcard KMS action" => [
    "- kms:Decrypt\n                  - kms:GenerateDataKey",
    '- "kms:*"'
  ],
  "provisioner key creation" => [
    "                Action: lambda:InvokeFunction",
    "                Action:\n                  - lambda:InvokeFunction\n                  - kms:CreateKey"
  ],
  "provisioner wildcard resource" => [
    "                Resource:\n                  Ref: RobinhoodKeyControlPlaneVersion",
    '                Resource: "*"'
  ],
  "mutable function invocation" => [
    "                Resource:\n                  Ref: RobinhoodKeyControlPlaneVersion",
    "                Resource:\n                  Fn::GetAtt:\n                    - RobinhoodKeyControlPlane\n                    - Arn"
  ],
  "signer identity policy" => [
    "  RobinhoodSignerRole:\n    Type: AWS::IAM::Role\n    Properties:\n      MaxSessionDuration: 3600",
    "  RobinhoodSignerRole:\n    Type: AWS::IAM::Role\n    Properties:\n      Policies:\n        - PolicyName: unsafe-signing\n          PolicyDocument:\n            Version: \"2012-10-17\"\n            Statement:\n              - Effect: Allow\n                Action: kms:Sign\n                Resource: \"*\"\n      MaxSessionDuration: 3600"
  ],
  "removed signer algorithm" => [
    '                                  "kms:SigningAlgorithm": "ECDSA_SHA_256",' + "\n",
    ""
  ],
  "changed statement effect" => [
    "              - Sid: InvokeFixedKeyControlPlane\n                Effect: Allow",
    "              - Sid: InvokeFixedKeyControlPlane\n                Effect: Deny"
  ],
  "mutable Lighter alias" => [
    "  LighterEnvelopeAlias:\n    Type: AWS::KMS::Alias\n    DeletionPolicy: Retain\n    UpdateReplacePolicy: Retain",
    "  LighterEnvelopeAlias:\n    Type: AWS::KMS::Alias"
  ],
  "caller-controlled key policy" => [
    '                          "Action": "kms:GetPublicKey",',
    '                          "Action": event["keyAction"],'
  ],
  "removed account binding" => [
    '                  "Id": f"robinhood-execution-key-{value}",',
    '                  "Id": "robinhood-execution-key",'
  ],
  "disabled lockout bypass" => [
    "                  BypassPolicyLockoutSafetyCheck=True,",
    "                  BypassPolicyLockoutSafetyCheck=False,"
  ],
  "enabled create retries" => [
    '              config=Config(retries={"total_max_attempts": 1, "mode": "standard"}),',
    '              config=Config(retries={"total_max_attempts": 3, "mode": "standard"}),'
  ],
  "removed policy escalation deny" => [
    "                  - kms:PutKeyPolicy\n",
    ""
  ],
  "removed tag mutation deny" => [
    "                  - kms:TagResource\n",
    ""
  ]
}

Dir.mktmpdir("aws-bootstrap-test") do |directory|
  mutations.each do |name, (before, after)|
    raise "missing mutation target for #{name}" unless template.include?(before)

    candidate = File.join(directory, "#{name.tr(" ", "-")}.yaml")
    File.write(candidate, template.sub(before, after))
    _stdout, _stderr, status = run_validator(validator, candidate)
    raise "validator accepted #{name}" if status.success?
  end
end

control_plane = YAML.safe_load(template, permitted_classes: [], aliases: false)
  .dig("Resources", "RobinhoodKeyControlPlane", "Properties", "Code", "ZipFile")
harness = <<~'PYTHON'
  import copy
  import json
  import os
  import sys
  import types

  class ClientError(Exception):
      def __init__(self, code):
          self.response = {"Error": {"Code": code}}

  class Paginator:
      def __init__(self, callback):
          self.callback = callback

      def paginate(self, **kwargs):
          return self.callback(**kwargs)

  class KMS:
      def __init__(self):
          self.keys = {}
          self.aliases = {}
          self.create_count = 0
          self.fail_after_create = False

      def create_key(self, **kwargs):
          assert kwargs["BypassPolicyLockoutSafetyCheck"] is True
          self.create_count += 1
          key_id = f"{self.create_count:08x}-1111-4111-8111-111111111111"
          arn = f"arn:aws:kms:us-east-1:123456789012:key/{key_id}"
          metadata = {
              "Arn": arn,
              "Description": kwargs["Description"],
              "KeySpec": kwargs["KeySpec"],
              "KeyUsage": kwargs["KeyUsage"],
              "Origin": kwargs["Origin"],
              "KeyManager": "CUSTOMER",
              "KeyState": "Enabled",
              "MultiRegion": kwargs["MultiRegion"],
          }
          self.keys[arn] = {
              "metadata": metadata,
              "policy": json.loads(kwargs["Policy"]),
              "aliases": set(),
          }
          if self.fail_after_create:
              self.fail_after_create = False
              raise RuntimeError("response lost")
          return {"KeyMetadata": copy.deepcopy(metadata)}

      def describe_key(self, KeyId):
          arn = self.aliases.get(KeyId, KeyId)
          if arn not in self.keys:
              raise ClientError("NotFoundException")
          return {"KeyMetadata": copy.deepcopy(self.keys[arn]["metadata"])}

      def get_key_policy(self, KeyId, PolicyName):
          assert PolicyName == "default"
          return {"Policy": json.dumps(self.keys[KeyId]["policy"])}

      def get_public_key(self, KeyId):
          assert KeyId in self.keys
          return {
              "KeySpec": "ECC_SECG_P256K1",
              "KeyUsage": "SIGN_VERIFY",
              "SigningAlgorithms": ["ECDSA_SHA_256"],
              "PublicKey": b"\x30\x00",
          }

      def get_paginator(self, operation):
          assert operation == "list_aliases"
          return Paginator(lambda KeyId: [{
              "Aliases": [
                  {"AliasName": alias}
                  for alias in sorted(self.keys[KeyId]["aliases"])
              ]
          }])

      def create_alias(self, AliasName, TargetKeyId):
          if AliasName in self.aliases:
              raise ClientError("AlreadyExistsException")
          self.aliases[AliasName] = TargetKeyId
          self.keys[TargetKeyId]["aliases"].add(AliasName)

  class DDB:
      def __init__(self):
          self.items = {}

      def get_item(self, TableName, Key, ConsistentRead):
          assert TableName == "ledger" and ConsistentRead
          value = Key["executionAccountId"]["S"]
          item = self.items.get(value)
          return {"Item": copy.deepcopy(item)} if item else {}

      def put_item(self, TableName, Item, ConditionExpression):
          assert TableName == "ledger"
          value = Item["executionAccountId"]["S"]
          if value in self.items:
              raise ClientError("ConditionalCheckFailedException")
          self.items[value] = copy.deepcopy(Item)

      def update_item(
          self,
          TableName,
          Key,
          UpdateExpression,
          ConditionExpression,
          ExpressionAttributeNames,
          ExpressionAttributeValues,
      ):
          assert TableName == "ledger"
          value = Key["executionAccountId"]["S"]
          item = self.items[value]
          state = item["state"]["S"]
          key_arn = ExpressionAttributeValues[":key"]["S"]
          if state not in {"CREATING", "ACTIVE"} or item.get("keyArn", {}).get("S") not in {None, key_arn}:
              raise ClientError("ConditionalCheckFailedException")
          item["keyArn"] = {"S": key_arn}
          if "SET #state" in UpdateExpression:
              item["state"] = {"S": "ACTIVE"}

  kms = KMS()
  ddb = DDB()

  class Config:
      def __init__(self, retries):
          assert retries == {"total_max_attempts": 1, "mode": "standard"}

  boto3 = types.ModuleType("boto3")
  def client(name, config=None):
      if name == "kms":
          return kms
      assert config is None
      return {"dynamodb": ddb}[name]
  boto3.client = client
  botocore = types.ModuleType("botocore")
  botocore_config = types.ModuleType("botocore.config")
  botocore_config.Config = Config
  exceptions = types.ModuleType("botocore.exceptions")
  exceptions.ClientError = ClientError
  sys.modules["boto3"] = boto3
  sys.modules["botocore"] = botocore
  sys.modules["botocore.config"] = botocore_config
  sys.modules["botocore.exceptions"] = exceptions
  os.environ.update({
      "ROBIN_ACCOUNT_ID": "123456789012",
      "ROBIN_PARTITION": "aws",
      "ROBIN_KEY_LEDGER": "ledger",
      "ROBIN_CONTROL_ROLE_ARN": "arn:aws:iam::123456789012:role/control",
      "ROBIN_SIGNER_ROLE_ARN": "arn:aws:iam::123456789012:role/signer",
  })

  namespace = {}
  exec(sys.stdin.read(), namespace)
  handler = namespace["handler"]
  account = "11111111-1111-4111-8111-111111111111"
  first = handler({"executionAccountId": account}, None)
  second = handler({"executionAccountId": account}, None)
  assert first == second
  assert kms.create_count == 1
  assert set(first) == {
      "executionAccountId",
      "keyArn",
      "alias",
      "keySpec",
      "keyUsage",
      "origin",
      "publicKey",
  }
  policy = kms.keys[first["keyArn"]]["policy"]
  assert policy["Id"] == f"robinhood-execution-key-{account}"
  assert policy["Statement"][1]["Principal"]["AWS"] == "arn:aws:iam::123456789012:role/control"
  assert policy["Statement"][3]["Principal"]["AWS"] == "arn:aws:iam::123456789012:role/signer"

  for request in [
      {},
      {"executionAccountId": account, "policy": {}},
      {"executionAccountId": "11111111-1111-1111-8111-111111111111"},
      {"executionAccountId": "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"},
  ]:
      try:
          handler(request, None)
      except ValueError:
          pass
      else:
          raise AssertionError("unsafe key request accepted")

  key_arn = first["keyArn"]
  saved_policy = kms.keys[key_arn]["policy"]
  kms.keys[key_arn]["policy"] = {"Version": "2012-10-17", "Statement": []}
  try:
      handler({"executionAccountId": account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("policy mismatch accepted")
  kms.keys[key_arn]["policy"] = saved_policy

  extra_alias = "alias/robinhood/execution/22222222-2222-4222-8222-222222222222"
  kms.keys[key_arn]["aliases"].add(extra_alias)
  try:
      handler({"executionAccountId": account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("alias mismatch accepted")
  kms.keys[key_arn]["aliases"].remove(extra_alias)

  ddb.items[account]["keyArn"] = {
      "S": "arn:aws:kms:us-east-1:123456789012:key/ffffffff-ffff-4fff-8fff-ffffffffffff"
  }
  try:
      handler({"executionAccountId": account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("ledger key mismatch accepted")
  ddb.items[account]["keyArn"] = {"S": key_arn}

  interrupted_account = "33333333-3333-4333-8333-333333333333"
  ddb.items[interrupted_account] = {
      "executionAccountId": {"S": interrupted_account},
      "state": {"S": "CREATING"},
  }
  interrupted_key = namespace["create_key"](interrupted_account)
  assert kms.keys[interrupted_key]["policy"]["Id"] == f"robinhood-execution-key-{interrupted_account}"
  try:
      handler({"executionAccountId": interrupted_account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("ambiguous interrupted creation retried")
  assert kms.create_count == 2

  ambiguous_account = "44444444-4444-4444-8444-444444444444"
  kms.fail_after_create = True
  try:
      handler({"executionAccountId": ambiguous_account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("lost CreateKey response accepted")
  assert kms.create_count == 3
  try:
      handler({"executionAccountId": ambiguous_account}, None)
  except RuntimeError:
      pass
  else:
      raise AssertionError("ambiguous CreateKey response retried")
  assert kms.create_count == 3

  print("key control-plane behavior tests passed")
PYTHON

stdout, stderr, status = Open3.capture3("python3", "-c", harness, stdin_data: control_plane)
raise "#{stdout}#{stderr}" unless status.success? && stdout.include?("behavior tests passed")

puts "aws bootstrap validator tests passed"
