package detect

import "strings"

type javaPack struct{}

func (javaPack) ID() Kind { return KindJava }

func (javaPack) Match(idx fileIndex) bool {
	return idx.hasAny("pom.xml", "build.gradle", "build.gradle.kts")
}

func (javaPack) Analyze(idx fileIndex) (Result, error) {
	manifest := "pom.xml"
	switch {
	case idx.has("build.gradle.kts"):
		manifest = "build.gradle.kts"
	case idx.has("build.gradle"):
		manifest = "build.gradle"
	}
	res := withPort(baseResult(KindJava, "java", manifest, "Java project"), DefaultPort, "pack")
	res.Framework = javaFramework(idx)
	res.GeneratedDockerfile = javaDockerfile(idx)
	return res, nil
}

func javaFramework(idx fileIndex) string {
	for _, name := range []string{"pom.xml", "build.gradle", "build.gradle.kts"} {
		raw, err := idx.read(name)
		if err != nil {
			continue
		}
		s := strings.ToLower(string(raw))
		if strings.Contains(s, "spring-boot") || strings.Contains(s, "org.springframework.boot") {
			return "spring-boot"
		}
	}
	return ""
}

func javaDockerfile(idx fileIndex) string {
	if idx.has("pom.xml") {
		return `FROM maven:3.9-eclipse-temurin-21
WORKDIR /app
COPY . .
RUN if [ -x ./mvnw ]; then ./mvnw -q -DskipTests package; else mvn -q -DskipTests package; fi
ENV PORT=8080
EXPOSE 8080
CMD ["sh","-c","java -jar target/*.jar"]
`
	}
	return `FROM gradle:8-jdk21
WORKDIR /app
COPY . .
RUN if [ -x ./gradlew ]; then ./gradlew -q build -x test; else gradle -q build -x test; fi
ENV PORT=8080
EXPOSE 8080
CMD ["sh","-c","java -jar build/libs/*.jar"]
`
}
