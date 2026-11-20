import pulumi
import pulumi_random

value = pulumi_random.RandomString("example", length=12, special=False)
pulumi.export("value", value.result)
pulumi.export("proof", "runner-image-contract")
