# One EC2 host for a personal Forge. Not high availability.
#
# Public ports are 80 and 443 only. Postgres, Redis, the Docker daemon,
# the API, the dashboard, and Caddy's admin API are not in the security
# group. Administration is SSM Session Manager. SSH is off unless
# admin_cidr and key_name are both set.
#
# Apply from this directory after `terraform init`. This file does not
# contain passwords, tokens, or a Forge .env.

terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "instance_type" {
  type        = string
  default     = "t3.small"
  description = "t3.micro is Free Tier eligible and often OOMs during docker build. t3.small plus the 2G swap in user-data is the practical minimum."
}

variable "admin_cidr" {
  type        = string
  default     = ""
  description = "Optional CIDR allowed to SSH. Empty means port 22 stays closed."
}

variable "key_name" {
  type        = string
  default     = ""
  description = "Optional EC2 key pair name. Ignored when admin_cidr is empty."
}

variable "apps_domain" {
  type        = string
  description = "Zone for *.apps hostnames, for example forge.com. Used only in outputs."
}

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023*-x86_64"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_vpc" "forge" {
  cidr_block           = "10.20.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = "forge" }
}

resource "aws_internet_gateway" "forge" {
  vpc_id = aws_vpc.forge.id
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.forge.id
  cidr_block              = "10.20.1.0/24"
  map_public_ip_on_launch = true
  availability_zone       = data.aws_availability_zones.available.names[0]
  tags                    = { Name = "forge-public" }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.forge.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.forge.id
  }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

resource "aws_security_group" "forge" {
  name        = "forge-ingress"
  description = "HTTP and HTTPS only. Data stores stay on the host."
  vpc_id      = aws_vpc.forge.id

  ingress {
    description = "HTTP ACME and redirects"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTPS apps and dashboard"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  dynamic "ingress" {
    for_each = var.admin_cidr == "" ? [] : [var.admin_cidr]
    content {
      description = "SSH from the operator CIDR"
      from_port   = 22
      to_port     = 22
      protocol    = "tcp"
      cidr_blocks = [ingress.value]
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_iam_role" "forge" {
  name = "forge-ec2"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.forge.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "forge" {
  name = "forge-ec2"
  role = aws_iam_role.forge.name
}

resource "aws_instance" "forge" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.forge.id]
  iam_instance_profile   = aws_iam_instance_profile.forge.name
  key_name               = var.key_name == "" ? null : var.key_name
  user_data              = file("${path.module}/user-data.sh")

  root_block_device {
    volume_type = "gp3"
    volume_size = 30
    encrypted   = true
  }

  metadata_options {
    http_tokens = "required"
  }

  tags = { Name = "forge" }
}

resource "aws_eip" "forge" {
  domain   = "vpc"
  instance = aws_instance.forge.id
  tags     = { Name = "forge" }
}

output "public_ip" {
  value = aws_eip.forge.public_ip
}

output "dns_hint" {
  value = "Point A records for *.${var.apps_domain} and the control host at ${aws_eip.forge.public_ip}. Do not open 5432, 6379, 2019, 3000, or 8080."
}
