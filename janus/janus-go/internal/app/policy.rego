package janus.authz

default allow := false

allow if {
	input.action == "profile.read"
	input.subject != ""
	input.resource.subject == input.subject
}

allow if {
	input.action == "account.register"
	input.subject == ""
}

allow if {
	input.action == "account.login"
	input.subject == ""
}

allow if {
	input.action == "account.google.login"
	input.subject == ""
}

allow if {
	input.action == "account.github.login"
	input.subject == ""
}

allow if {
	input.action == "account.github.connect"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.create"
	input.subject != ""
}

allow if {
	input.action == "project.read"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.import"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.build"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.run"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.update"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "project.delete"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "account.delete"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "agent.code.create"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "agent.read"
	input.subject != ""
	input.resource.owner_id == input.subject
}

allow if {
	input.action == "agent.remove"
	input.subject != ""
	input.resource.owner_id == input.subject
}

# An Agent enrolls from its command line with no session. The Enrollment Code
# it presents is the credential.
allow if {
	input.action == "agent.enroll"
	input.subject == ""
}
