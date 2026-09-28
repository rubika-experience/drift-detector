// Jenkinsfile — build/vet/test pipeline for driftgcp.
//
// This is a scaffold: point a Jenkins job at this repo (branch
// rubika-venkatesh/driftgcp or main) and it runs on the QA Jenkins agent.
// No deploy stage yet — add one once there's a real target to ship to.
pipeline {
    agent any

    options {
        timestamps()
        disableConcurrentBuilds()
    }

    environment {
        GOFLAGS = '-mod=mod'
    }

    stages {
        stage('Build') {
            steps {
                sh 'go build ./...'
            }
        }

        stage('Vet') {
            steps {
                sh 'go vet ./...'
            }
        }

        stage('Test') {
            steps {
                sh 'go test ./... -v'
            }
        }
    }

    post {
        always {
            sh 'go version'
        }
        failure {
            echo 'Build failed — check the Build/Vet/Test stage logs above.'
        }
    }
}
